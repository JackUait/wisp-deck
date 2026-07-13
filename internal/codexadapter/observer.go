package codexadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"sync"

	"github.com/coder/websocket"
)

type ObserverConfig struct {
	SocketPath    string
	ClientVersion string
}

type Observer struct {
	config ObserverConfig
	client *http.Client
}

func NewObserver(config ObserverConfig) (*Observer, error) {
	if !filepath.IsAbs(config.SocketPath) {
		return nil, errors.New("app-server socket path must be absolute")
	}
	if err := validateObserverString("client version", config.ClientVersion, MaxReducerIDBytes, false); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	transport := &http.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: false,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", config.SocketPath)
		},
	}
	return &Observer{config: config, client: &http.Client{Transport: transport}}, nil
}

type ObserverSession struct {
	conn *websocket.Conn

	mu       sync.Mutex
	closed   bool
	snapshot []Thread
	threads  map[string]Thread
}

func (o *Observer) Open(ctx context.Context) (*ObserverSession, error) {
	conn, response, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{
		HTTPClient:      o.client,
		CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("connect app-server websocket: %w", err)
	}
	conn.SetReadLimit(MaxObserverMessageBytes)
	session := &ObserverSession{conn: conn, threads: make(map[string]Thread)}
	if err := session.initializeAndSnapshot(ctx, o.config.ClientVersion); err != nil {
		conn.CloseNow()
		return nil, err
	}
	return session, nil
}

func (s *ObserverSession) Snapshot() []Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneThreads(s.snapshot)
}

func (s *ObserverSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.conn.CloseNow()
}

func (s *ObserverSession) Next(ctx context.Context) (ReducerEvent, error) {
	for {
		envelope, err := s.readEnvelope(ctx)
		if err != nil {
			return ReducerEvent{}, err
		}
		if len(envelope.ID) != 0 && envelope.Method == "" {
			return ReducerEvent{}, errors.New("unexpected app-server response without an outstanding request")
		}
		event, recognized, err := decodeNotification(envelope)
		if err != nil {
			return ReducerEvent{}, err
		}
		if !recognized {
			continue
		}
		if err := s.applyLive(event); err != nil {
			return ReducerEvent{}, err
		}
		return event, nil
	}
}

type journalEntry struct {
	event      ReducerEvent
	readUpsert bool
}

func (s *ObserverSession) initializeAndSnapshot(ctx context.Context, version string) error {
	journal := make([]journalEntry, 0, 32)
	appendNotification := func(event ReducerEvent) error {
		if len(journal) >= MaxObserverJournal {
			return errors.New("observer snapshot journal limit exceeded")
		}
		journal = append(journal, journalEntry{event: event})
		return nil
	}

	initializeResultRaw, err := s.request(ctx, "wisp-1", methodInitialize, map[string]any{
		"clientInfo": map[string]any{
			"name": "codex_app_server_daemon", "title": "Wisp Deck passive observer", "version": version,
		},
		"capabilities": map[string]any{
			"experimentalApi": false, "requestAttestation": false, "mcpServerOpenaiFormElicitation": false,
		},
	}, appendNotification)
	if err != nil {
		return err
	}
	if err := decodeInitializeResult(initializeResultRaw); err != nil {
		return err
	}
	if err := s.writeObject(ctx, map[string]any{"method": methodInitialized}); err != nil {
		return err
	}

	loaded := make([]string, 0, loadedListPageSize)
	loadedSet := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	var cursor *string
	nextID := 2
	pageCount := 0
	for {
		pageCount++
		if pageCount > MaxObserverThreads {
			return errors.New("loaded-list page limit exceeded")
		}
		params := map[string]any{"cursor": nil, "limit": loadedListPageSize}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		id := fmt.Sprintf("wisp-%d", nextID)
		nextID++
		resultRaw, err := s.request(ctx, id, methodThreadLoadedList, params, appendNotification)
		if err != nil {
			return err
		}
		ids, nextCursor, err := decodeLoadedListResult(resultRaw)
		if err != nil {
			return err
		}
		for _, threadID := range ids {
			if _, duplicate := loadedSet[threadID]; duplicate {
				return fmt.Errorf("duplicate loaded thread id %q", threadID)
			}
			if len(loaded) >= MaxObserverThreads {
				return errors.New("loaded thread limit exceeded")
			}
			loadedSet[threadID] = struct{}{}
			loaded = append(loaded, threadID)
		}
		if nextCursor == nil {
			break
		}
		if _, repeated := seenCursors[*nextCursor]; repeated {
			return fmt.Errorf("repeated loaded-list cursor %q", *nextCursor)
		}
		seenCursors[*nextCursor] = struct{}{}
		cursor = nextCursor
	}

	for _, threadID := range loaded {
		id := fmt.Sprintf("wisp-%d", nextID)
		nextID++
		resultRaw, err := s.request(ctx, id, methodThreadRead, map[string]any{
			"threadId": threadID, "includeTurns": false,
		}, appendNotification)
		if err != nil {
			return err
		}
		thread, err := decodeThreadReadResult(resultRaw)
		if err != nil {
			return err
		}
		if thread.ID != threadID {
			return fmt.Errorf("thread/read returned %q for %q", thread.ID, threadID)
		}
		if len(journal) >= MaxObserverJournal {
			return errors.New("observer snapshot journal limit exceeded")
		}
		journal = append(journal, journalEntry{
			event: ReducerEvent{Kind: EventThreadObserved, Thread: thread}, readUpsert: true,
		})
	}

	candidate, err := replaySnapshotJournal(journal, loadedSet)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.threads = candidate
	s.snapshot = sortedThreads(candidate)
	s.mu.Unlock()
	return nil
}

func (s *ObserverSession) request(
	ctx context.Context,
	id string,
	method string,
	params any,
	appendNotification func(ReducerEvent) error,
) (json.RawMessage, error) {
	if !allowedOutboundMethod(method) {
		return nil, fmt.Errorf("outbound app-server method %q is not passive", method)
	}
	request := map[string]any{"id": id, "method": method, "params": params}
	if err := s.writeObject(ctx, request); err != nil {
		return nil, err
	}
	for {
		envelope, err := s.readEnvelope(ctx)
		if err != nil {
			return nil, err
		}
		if len(envelope.ID) != 0 && envelope.Method == "" {
			responseID, err := rawIDString(envelope.ID)
			if err != nil {
				return nil, err
			}
			if responseID != id {
				return nil, fmt.Errorf("unexpected response id %q while waiting for %q", responseID, id)
			}
			if len(envelope.Error) != 0 && !stringIsJSONNull(envelope.Error) {
				return nil, fmt.Errorf("app-server %s returned an error", method)
			}
			if len(envelope.Result) == 0 {
				return nil, fmt.Errorf("app-server %s response is missing result", method)
			}
			return envelope.Result, nil
		}
		event, recognized, err := decodeNotification(envelope)
		if err != nil {
			return nil, err
		}
		if recognized {
			if err := appendNotification(event); err != nil {
				return nil, err
			}
		}
	}
}

func (s *ObserverSession) readEnvelope(ctx context.Context) (wireEnvelope, error) {
	messageType, payload, err := s.conn.Read(ctx)
	if err != nil {
		return wireEnvelope{}, err
	}
	if messageType != websocket.MessageText {
		return wireEnvelope{}, errors.New("app-server sent a binary websocket message")
	}
	return decodeWireEnvelope(payload)
}

func (s *ObserverSession) writeObject(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.conn.Write(ctx, websocket.MessageText, payload)
}

func allowedOutboundMethod(method string) bool {
	switch method {
	case methodInitialize, methodInitialized, methodThreadLoadedList, methodThreadRead:
		return true
	default:
		return false
	}
}

func replaySnapshotJournal(journal []journalEntry, loaded map[string]struct{}) (map[string]Thread, error) {
	threads := make(map[string]Thread)
	closed := make(map[string]struct{})
	started := make(map[string]struct{})
	for _, entry := range journal {
		event := entry.event
		switch event.Kind {
		case EventThreadObserved:
			if _, wasClosed := closed[event.Thread.ID]; wasClosed {
				return nil, fmt.Errorf("thread %q was read or started after close", event.Thread.ID)
			}
			if !entry.readUpsert {
				if _, duplicate := started[event.Thread.ID]; duplicate {
					return nil, fmt.Errorf("duplicate thread/started for %q", event.Thread.ID)
				}
				started[event.Thread.ID] = struct{}{}
			}
			if _, exists := threads[event.Thread.ID]; !exists && len(threads) >= MaxObserverThreads {
				return nil, errors.New("snapshot thread limit exceeded")
			}
			threads[event.Thread.ID] = cloneObservedThread(event.Thread)

		case EventThreadStatus:
			thread, exists := threads[event.ThreadID]
			if !exists {
				if _, willBeRead := loaded[event.ThreadID]; willBeRead {
					// The later synthetic read is authoritative at its exact wire
					// position, so this update has no surviving field to mutate.
					continue
				}
				return nil, fmt.Errorf("status for unknown thread %q", event.ThreadID)
			}
			thread.Status = cloneObservedStatus(event.Status)
			threads[event.ThreadID] = thread

		case EventThreadClosed:
			if _, exists := threads[event.ThreadID]; !exists {
				if _, willBeRead := loaded[event.ThreadID]; !willBeRead {
					return nil, fmt.Errorf("close for unknown thread %q", event.ThreadID)
				}
			}
			delete(threads, event.ThreadID)
			closed[event.ThreadID] = struct{}{}

		default:
			return nil, errors.New("unsupported event in observer snapshot journal")
		}
	}
	return threads, nil
}

func (s *ObserverSession) applyLive(event ReducerEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch event.Kind {
	case EventThreadObserved:
		if _, exists := s.threads[event.Thread.ID]; exists {
			return fmt.Errorf("duplicate live thread/started for %q", event.Thread.ID)
		}
		if len(s.threads) >= MaxObserverThreads {
			return errors.New("live thread limit exceeded")
		}
		s.threads[event.Thread.ID] = cloneObservedThread(event.Thread)
	case EventThreadStatus:
		thread, exists := s.threads[event.ThreadID]
		if !exists {
			return fmt.Errorf("live status for unknown thread %q", event.ThreadID)
		}
		thread.Status = cloneObservedStatus(event.Status)
		s.threads[event.ThreadID] = thread
	case EventThreadClosed:
		if _, exists := s.threads[event.ThreadID]; !exists {
			return fmt.Errorf("live close for unknown thread %q", event.ThreadID)
		}
		delete(s.threads, event.ThreadID)
	default:
		return errors.New("unsupported live observer event")
	}
	return nil
}

func stringIsJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

func sortedThreads(threads map[string]Thread) []Thread {
	ids := make([]string, 0, len(threads))
	for id := range threads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]Thread, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneObservedThread(threads[id]))
	}
	return result
}

func cloneThreads(threads []Thread) []Thread {
	result := make([]Thread, len(threads))
	for i := range threads {
		result[i] = cloneObservedThread(threads[i])
	}
	return result
}

func cloneObservedThread(thread Thread) Thread {
	thread.Status = cloneObservedStatus(thread.Status)
	return thread
}

func cloneObservedStatus(status ThreadStatus) ThreadStatus {
	status.ActiveFlags = append([]ActiveFlag(nil), status.ActiveFlags...)
	return status
}
