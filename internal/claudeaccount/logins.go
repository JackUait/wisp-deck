package claudeaccount

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// LoginStore is where Claude Code keeps each slot's OAuth login: the
// claudeAiOauth object of the slot's Keychain entry. configDir is "" for the
// default slot. The real store lives in internal/allin, which owns the
// Keychain; this package only decides what moves where.
type LoginStore interface {
	Login(configDir string) (json.RawMessage, error)
	SetLogin(configDir string, login json.RawMessage) error
	Lock(configDir string) (release func(), err error)
}

// ReconcileOptions names the files one Reconcile reads and repairs.
type ReconcileOptions struct {
	ListFile    string
	AccountsDir string
	// EmailsFile holds one dir:email pin per slot, keyed like the colors file.
	EmailsFile string
	// UsageDir holds each slot's cached usage as <dir>.json. Optional.
	UsageDir string
	// HomeDir is where the default slot keeps .claude.json.
	HomeDir string
	// Store is nil for a caller that may not touch the host: slots are still
	// pinned, but nothing moves.
	Store LoginStore
}

type loginSlot struct {
	key       string
	configDir string
	stateFile string
	email     string
}

// LoadEmails parses a dir:email pin file.
func LoadEmails(file string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(file)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, email, ok := strings.Cut(line, ":"); ok && key != "" && email != "" {
			out[key] = email
		}
	}
	return out
}

func saveEmails(file string, pins map[string]string) error {
	keys := make([]string, 0, len(pins))
	for k := range pins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s:%s\n", k, pins[k])
	}
	return writeFileAtomic(file, []byte(b.String()), 0o644)
}

// Reconcile keeps every slot on the login it was first seen with.
//
// `/login` writes into whichever slot the pane runs under, so logging into the
// wrong pane crosses two slots. Each slot is pinned to its email on first
// sight. When slots hold each other's pinned emails (a swap or a longer
// cycle), their logins are moved back. A slot on an email no slot is pinned to
// is a deliberate change and is re-pinned. Anything else (two slots on one
// email) has lost a token and is left alone.
//
// The email is read from .claude.json only, so a deck where every slot matches
// never touches the Keychain.
func Reconcile(opts ReconcileOptions) error {
	slots := loginSlots(opts)
	pins := LoadEmails(opts.EmailsFile)
	pinnedTo := map[string]string{}
	for key, email := range pins {
		pinnedTo[strings.ToLower(email)] = key
	}

	byKey := map[string]*loginSlot{}
	for i := range slots {
		byKey[slots[i].key] = &slots[i]
	}

	pinsChanged := false
	moves := map[string]string{}
	for i := range slots {
		s := &slots[i]
		if s.email == "" {
			continue
		}
		pin, pinned := pins[s.key]
		if pinned && strings.EqualFold(pin, s.email) {
			continue
		}
		owner, taken := pinnedTo[strings.ToLower(s.email)]
		switch {
		case !taken:
			pins[s.key] = s.email
			pinsChanged = true
		case pinned && byKey[owner] != nil:
			moves[s.key] = owner
		}
	}
	// Only a full permutation is moved: every source must also be a target,
	// or some slot's own login would be overwritten with nowhere to go.
	if !isPermutation(moves) {
		moves = nil
	}

	if pinsChanged {
		if err := saveEmails(opts.EmailsFile, pins); err != nil {
			return err
		}
	}
	if len(moves) == 0 || opts.Store == nil {
		return nil
	}
	return moveLogins(opts, byKey, moves)
}

func isPermutation(moves map[string]string) bool {
	seen := map[string]bool{}
	for _, dst := range moves {
		if seen[dst] {
			return false
		}
		if _, ok := moves[dst]; !ok {
			return false
		}
		seen[dst] = true
	}
	return true
}

func loginSlots(opts ReconcileOptions) []loginSlot {
	slots := []loginSlot{{key: "default", stateFile: filepath.Join(opts.HomeDir, ".claude.json")}}
	for _, a := range Load(opts.ListFile) {
		if a.Dir == "" {
			continue
		}
		dir := filepath.Join(opts.AccountsDir, a.Dir)
		slots = append(slots, loginSlot{key: a.Dir, configDir: dir, stateFile: filepath.Join(dir, ".claude.json")})
	}
	for i := range slots {
		slots[i].email = stateEmail(slots[i].stateFile)
	}
	return slots
}

func stateEmail(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var state struct {
		OAuth struct {
			Email string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &state) != nil {
		return ""
	}
	return strings.TrimSpace(state.OAuth.Email)
}

// moveLogins sends each source slot's login to its target. Everything is read
// before anything is written, so a cycle never reads a login it already
// overwrote.
func moveLogins(opts ReconcileOptions, byKey map[string]*loginSlot, moves map[string]string) error {
	keys := make([]string, 0, len(moves))
	for k := range moves {
		keys = append(keys, k)
	}
	// A fixed order keeps two reconciles from locking the same slots in
	// opposite orders.
	sort.Strings(keys)
	for _, k := range keys {
		release, err := opts.Store.Lock(byKey[k].configDir)
		if err != nil {
			return err
		}
		defer release()
	}

	logins := map[string]json.RawMessage{}
	accounts := map[string]json.RawMessage{}
	usage := map[string][]byte{}
	for _, k := range keys {
		s := byKey[k]
		login, err := opts.Store.Login(s.configDir)
		if err != nil {
			return err
		}
		logins[k] = login
		account, err := stateAccount(s.stateFile)
		if err != nil {
			return err
		}
		accounts[k] = account
		if opts.UsageDir != "" {
			if data, err := os.ReadFile(filepath.Join(opts.UsageDir, k+".json")); err == nil {
				usage[k] = data
			}
		}
	}

	for _, src := range keys {
		dst := byKey[moves[src]]
		if err := opts.Store.SetLogin(dst.configDir, logins[src]); err != nil {
			return err
		}
		if err := setStateAccount(dst.stateFile, accounts[src]); err != nil {
			return err
		}
		if opts.UsageDir == "" {
			continue
		}
		// A cache left behind would show the other login's numbers until it
		// ages out.
		target := filepath.Join(opts.UsageDir, dst.key+".json")
		if data, ok := usage[src]; ok {
			if err := writeFileAtomic(target, data, 0o600); err != nil {
				return err
			}
		} else if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func stateAccount(file string) (json.RawMessage, error) {
	state, err := readState(file)
	if err != nil {
		return nil, err
	}
	account, ok := state["oauthAccount"]
	if !ok {
		return nil, errors.New("claudeaccount: " + file + " has no oauthAccount")
	}
	return account, nil
}

func setStateAccount(file string, account json.RawMessage) error {
	state, err := readState(file)
	if err != nil {
		return err
	}
	state["oauthAccount"] = account
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(file, data, 0o600)
}

func readState(file string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	state := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("claudeaccount: parse %s: %w", file, err)
	}
	return state, nil
}

// writeFileAtomic keeps a reader (Claude Code, or a second reconcile) from
// ever seeing a half-written file.
func writeFileAtomic(file string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".tmp.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck -- gone after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck -- the write already failed
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// GatedReconcile returns a Reconcile that only runs again once a slot's
// files change. The All-In router calls it on every borrowed turn.
func GatedReconcile(opts ReconcileOptions) func() {
	return gatedReconcile(opts, Reconcile)
}

func gatedReconcile(opts ReconcileOptions, run func(ReconcileOptions) error) func() {
	var mu sync.Mutex
	last := ""
	return func() {
		mu.Lock()
		defer mu.Unlock()
		sig := loginFilesSignature(opts)
		if sig == last {
			return
		}
		// A failed run is retried on the next call rather than cached.
		if run(opts) == nil {
			last = loginFilesSignature(opts)
		}
	}
}

// loginFilesSignature is size and mtime of every file Reconcile reads.
func loginFilesSignature(opts ReconcileOptions) string {
	files := []string{opts.ListFile, opts.EmailsFile, filepath.Join(opts.HomeDir, ".claude.json")}
	for _, a := range Load(opts.ListFile) {
		files = append(files, filepath.Join(opts.AccountsDir, a.Dir, ".claude.json"))
	}
	var b strings.Builder
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", f, info.Size(), info.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s:-;", f)
		}
	}
	return b.String()
}
