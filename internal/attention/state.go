// Package attention defines the small, generation-fenced protocol shared by
// Wisp Deck's agent adapters and notification consumer.
package attention

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	stateProtocolVersion = "1"

	// MaxStateRecordBytes bounds parser memory and rejects corrupted files before
	// splitting them. Normal records are well under one kilobyte.
	MaxStateRecordBytes = 4 * 1024
)

// ErrStaleGeneration means the controller removed the writer's generation
// directory. Adapters must stop publishing instead of recreating that parent.
var ErrStaleGeneration = errors.New("attention generation is stale")

type Phase string

const (
	PhaseReady     Phase = "ready"
	PhaseWorking   Phase = "working"
	PhaseAttention Phase = "attention"
	PhaseUnknown   Phase = "unknown"
)

type Reason string

const (
	ReasonNone       Reason = "-"
	ReasonDone       Reason = "done"
	ReasonQuestion   Reason = "question"
	ReasonPermission Reason = "permission"
	ReasonError      Reason = "error"
)

// State is the complete normalized state persisted by one adapter generation.
// Adapter-specific event identities remain in memory and are represented on
// disk only by Sequence advancing.
type State struct {
	Generation string
	Sequence   uint64
	Phase      Phase
	Reason     Reason
}

// ParseState parses one complete protocol record without accepting partial or
// extended forms.
func ParseState(data []byte) (State, error) {
	if len(data) == 0 {
		return State{}, errors.New("empty attention state")
	}
	if len(data) > MaxStateRecordBytes {
		return State{}, fmt.Errorf("attention state exceeds %d bytes", MaxStateRecordBytes)
	}
	if data[len(data)-1] != '\n' || bytes.Count(data, []byte{'\n'}) != 1 {
		return State{}, errors.New("attention state must have exactly one trailing newline")
	}
	if bytes.ContainsRune(data, '\r') {
		return State{}, errors.New("attention state contains carriage return")
	}

	fields := strings.Split(string(data[:len(data)-1]), "\t")
	if len(fields) != 5 {
		return State{}, fmt.Errorf("attention state has %d fields, want 5", len(fields))
	}
	if fields[0] != stateProtocolVersion {
		return State{}, fmt.Errorf("unsupported attention state version %q", fields[0])
	}
	if err := validateGeneration(fields[1]); err != nil {
		return State{}, err
	}
	sequence, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil || strconv.FormatUint(sequence, 10) != fields[2] {
		return State{}, fmt.Errorf("invalid attention sequence %q", fields[2])
	}

	state := State{
		Generation: fields[1],
		Sequence:   sequence,
		Phase:      Phase(fields[3]),
		Reason:     Reason(fields[4]),
	}
	if err := state.validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// MarshalText returns the canonical single-line protocol representation.
func (s State) MarshalText() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	record := []byte(fmt.Sprintf("%s\t%s\t%d\t%s\t%s\n",
		stateProtocolVersion, s.Generation, s.Sequence, s.Phase, s.Reason))
	if len(record) > MaxStateRecordBytes {
		return nil, fmt.Errorf("attention state exceeds %d bytes", MaxStateRecordBytes)
	}
	return record, nil
}

func (s State) validate() error {
	if err := validateGeneration(s.Generation); err != nil {
		return err
	}
	switch s.Phase {
	case PhaseReady, PhaseWorking, PhaseUnknown:
		if s.Reason != ReasonNone {
			return fmt.Errorf("phase %q requires reason %q", s.Phase, ReasonNone)
		}
	case PhaseAttention:
		switch s.Reason {
		case ReasonDone, ReasonQuestion, ReasonPermission, ReasonError:
		default:
			return fmt.Errorf("invalid attention reason %q", s.Reason)
		}
	default:
		return fmt.Errorf("invalid attention phase %q", s.Phase)
	}
	return nil
}

func validateGeneration(generation string) error {
	if generation == "" {
		return errors.New("attention generation is empty")
	}
	if strings.ContainsAny(generation, "\t\r\n") {
		return errors.New("attention generation contains a record delimiter")
	}
	return nil
}

// AtomicWriter serializes semantic changes into one generation's state file.
type AtomicWriter struct {
	mu sync.Mutex

	path       string
	generation string
	state      State
	hasState   bool

	lastIdentity  string
	identityKnown bool
}

// NewAtomicWriter opens a generation that was already created by the shell
// controller. It never creates the parent directory.
func NewAtomicWriter(path, generation string) (*AtomicWriter, error) {
	if path == "" {
		return nil, errors.New("attention state path is empty")
	}
	if err := validateGeneration(generation); err != nil {
		return nil, err
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrStaleGeneration
		}
		return nil, fmt.Errorf("stat attention generation: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrStaleGeneration
	}

	w := &AtomicWriter{
		path:       path,
		generation: generation,
		state: State{
			Generation: generation,
			Phase:      PhaseUnknown,
			Reason:     ReasonNone,
		},
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if recovered, parseErr := ParseState(data); parseErr == nil && recovered.Generation == generation {
			w.state = recovered
			w.hasState = true
			// Identity is intentionally absent from the on-disk protocol. The
			// first identical attention observation after restart attaches its
			// identity without advancing, preferring no duplicate alert.
			w.identityKnown = recovered.Phase != PhaseAttention
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read attention state: %w", err)
	}
	return w, nil
}

// Publish atomically persists a semantic change. Identity distinguishes two
// attention requests with the same visible phase/reason; it is ignored outside
// attention state.
func (w *AtomicWriter) Publish(phase Phase, reason Reason, identity string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	candidate := State{
		Generation: w.generation,
		Sequence:   w.state.Sequence,
		Phase:      phase,
		Reason:     reason,
	}
	if err := candidate.validate(); err != nil {
		return err
	}

	semanticChanged := !w.hasState || w.state.Phase != phase || w.state.Reason != reason
	identityChanged := false
	if !semanticChanged && phase == PhaseAttention && identity != "" {
		if !w.identityKnown {
			w.lastIdentity = identity
			w.identityKnown = true
			return nil
		}
		identityChanged = identity != w.lastIdentity
	}
	if !semanticChanged && !identityChanged {
		return nil
	}
	if w.state.Sequence == math.MaxUint64 {
		return errors.New("attention sequence overflow")
	}
	candidate.Sequence++
	record, err := candidate.MarshalText()
	if err != nil {
		return err
	}
	if err := atomicReplace(w.path, record); err != nil {
		return err
	}

	w.state = candidate
	w.hasState = true
	if phase == PhaseAttention && identity != "" {
		w.lastIdentity = identity
		w.identityKnown = true
	} else if phase == PhaseAttention {
		w.lastIdentity = ""
		w.identityKnown = false
	} else {
		w.lastIdentity = ""
		w.identityKnown = true
	}
	return nil
}

// Current returns the last successfully persisted state, or the generation's
// initial unknown state before the first publish.
func (w *AtomicWriter) Current() State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func atomicReplace(path string, data []byte) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		if err == nil || os.IsNotExist(err) {
			return ErrStaleGeneration
		}
		return fmt.Errorf("stat attention generation: %w", err)
	}

	tmp, err := os.CreateTemp(parent, ".attention-*")
	if err != nil {
		if os.IsNotExist(err) {
			return ErrStaleGeneration
		}
		return fmt.Errorf("create attention temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod attention temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write attention temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close attention temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if os.IsNotExist(err) {
			return ErrStaleGeneration
		}
		return fmt.Errorf("replace attention state: %w", err)
	}
	return nil
}

var _ encoding.TextMarshaler = State{}
