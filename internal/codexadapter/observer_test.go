package codexadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const observerTestVersion = "1.2.3-test"

type udsWSServer struct {
	t          *testing.T
	socketPath string
	listener   net.Listener
	server     *http.Server
	err        chan error
}

func startUDSWSServer(t *testing.T, handler func(context.Context, *websocket.Conn) error) *udsWSServer {
	t.Helper()
	// Darwin's sockaddr_un path is only 104 bytes. The production supervisor
	// deliberately uses the same short /tmp layout.
	dir, err := os.MkdirTemp("/tmp", "wdo.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "a.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}

	s := &udsWSServer{t: t, socketPath: socketPath, listener: listener, err: make(chan error, 1)}
	s.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.Host != "localhost" {
			s.err <- errors.New("unexpected websocket URL or Host")
			return
		}
		for _, header := range []string{"Origin", "Authorization", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions"} {
			if r.Header.Get(header) != "" {
				s.err <- errors.New("unexpected websocket header " + header)
				return
			}
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			s.err <- err
			return
		}
		defer conn.CloseNow()
		s.err <- handler(r.Context(), conn)
	})}
	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case s.err <- err:
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = s.server.Close()
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	return s
}

func wsReadObject(ctx context.Context, conn *websocket.Conn) (map[string]any, error) {
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if messageType != websocket.MessageText {
		return nil, errors.New("client sent a binary websocket message")
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func wsWriteObject(ctx context.Context, conn *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

func observerThread(id, sessionID, parent, cwd, status string, flags ...string) map[string]any {
	var parentValue any
	if parent != "" {
		parentValue = parent
	}
	statusValue := map[string]any{"type": status}
	if status == "active" {
		statusValue["activeFlags"] = flags
	}
	return map[string]any{
		"id": id, "sessionId": sessionID, "parentThreadId": parentValue,
		"cwd": cwd, "status": statusValue,
	}
}

func TestObserverUsesExactPassiveProtocolAndBuildsCoherentSnapshot(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
		readRequest := func() (map[string]any, error) {
			request, err := wsReadObject(ctx, conn)
			if err == nil {
				if _, exists := request["jsonrpc"]; exists {
					return nil, errors.New("jsonrpc field must be omitted")
				}
				if method, ok := request["method"].(string); ok {
					mu.Lock()
					methods = append(methods, method)
					mu.Unlock()
				}
			}
			return request, err
		}

		initialize, err := readRequest()
		if err != nil {
			return err
		}
		wantInitialize := map[string]any{
			"id": "wisp-1", "method": "initialize",
			"params": map[string]any{
				"clientInfo": map[string]any{
					"name": "codex_app_server_daemon", "title": "Wisp Deck passive observer", "version": observerTestVersion,
				},
				"capabilities": map[string]any{
					"experimentalApi": false, "requestAttestation": false, "mcpServerOpenaiFormElicitation": false,
				},
			},
		}
		if !reflect.DeepEqual(initialize, wantInitialize) {
			return errors.New("initialize request did not match the reserved daemon identity")
		}

		// A recognized notification may arrive before initialize completes. It
		// must participate in the same ordered candidate, not be discarded.
		if err := wsWriteObject(ctx, conn, map[string]any{
			"method": "thread/started",
			"params": map[string]any{"thread": observerThread("early", "early-session", "", "/other", "idle")},
		}); err != nil {
			return err
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": "wisp-1", "result": map[string]any{
				"userAgent": "codex-test", "codexHome": "/tmp/codex", "platformFamily": "unix", "platformOs": "macos",
			},
		}); err != nil {
			return err
		}

		initialized, err := readRequest()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(initialized, map[string]any{"method": "initialized"}) {
			return errors.New("initialized notification was not exact")
		}

		listOne, err := readRequest()
		if err != nil {
			return err
		}
		if listOne["method"] != "thread/loaded/list" || !reflect.DeepEqual(listOne["params"], map[string]any{"cursor": nil, "limit": float64(256)}) {
			return errors.New("first loaded-list page was malformed")
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": listOne["id"], "result": map[string]any{"data": []string{"root", "child"}, "nextCursor": "page-2"},
		}); err != nil {
			return err
		}

		listTwo, err := readRequest()
		if err != nil {
			return err
		}
		if listTwo["method"] != "thread/loaded/list" || !reflect.DeepEqual(listTwo["params"], map[string]any{"cursor": "page-2", "limit": float64(256)}) {
			return errors.New("second loaded-list page was malformed")
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": listTwo["id"], "result": map[string]any{"data": []string{}, "nextCursor": nil},
		}); err != nil {
			return err
		}

		readRoot, err := readRequest()
		if err != nil {
			return err
		}
		if readRoot["method"] != "thread/read" || !reflect.DeepEqual(readRoot["params"], map[string]any{"threadId": "root", "includeTurns": false}) {
			return errors.New("root read request was malformed")
		}
		// An equal notification before the read response is idempotent even though
		// response wire order alone does not reveal which value was sampled first.
		if err := wsWriteObject(ctx, conn, map[string]any{
			"method": "thread/status/changed", "params": map[string]any{
				"threadId": "root", "status": map[string]any{"type": "idle"},
			},
		}); err != nil {
			return err
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": readRoot["id"], "result": map[string]any{"thread": observerThread("root", "session", "", "/repo", "idle")},
		}); err != nil {
			return err
		}

		readChild, err := readRequest()
		if err != nil {
			return err
		}
		if readChild["method"] != "thread/read" || !reflect.DeepEqual(readChild["params"], map[string]any{"threadId": "child", "includeTurns": false}) {
			return errors.New("child read request was malformed")
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": readChild["id"], "result": map[string]any{"thread": observerThread("child", "session", "root", "/repo", "idle")},
		}); err != nil {
			return err
		}

		// Initialized connections may receive requests for some newly attached
		// threads, but that is not a stable or complete request census. Inject one
		// to prove it is ignored and never answered; the following global status
		// notification remains the observer's interaction truth.
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": 91, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "child"},
		}); err != nil {
			return err
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"method": "thread/status/changed", "params": map[string]any{
				"threadId": "child", "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnUserInput"}},
			},
		}); err != nil {
			return err
		}
		readCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		if response, err := wsReadObject(readCtx, conn); err == nil {
			return errors.New("passive observer answered server request: " + response["method"].(string))
		}
		return nil
	})

	observer, err := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := observer.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	snapshot := session.Snapshot()
	if got := threadIDs(snapshot); !reflect.DeepEqual(got, []string{"child", "early", "root"}) {
		t.Fatalf("snapshot IDs = %v, want [child early root]", got)
	}
	for _, thread := range snapshot {
		if thread.ID == "root" && thread.Status.Type != ThreadStatusIdle {
			t.Fatalf("root status = %q, want later read's idle", thread.Status.Type)
		}
	}

	event, err := session.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventThreadStatus || event.ThreadID != "child" || event.Status.Type != ThreadStatusActive {
		t.Fatalf("live event = %#v", event)
	}

	select {
	case err := <-server.err:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("fake app server did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	wantMethods := []string{"initialize", "initialized", "thread/loaded/list", "thread/loaded/list", "thread/read", "thread/read"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("client methods = %v, want %v", methods, wantMethods)
	}
}

func TestObserverSnapshotCarriesPreexistingPendingStatusFromThreadRead(t *testing.T) {
	const rootID = "11111111-1111-4111-8111-111111111111"
	server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
		if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
			return err
		}
		list, err := wsReadObject(ctx, conn)
		if err != nil {
			return err
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": list["id"], "result": map[string]any{"data": []string{rootID}, "nextCursor": nil},
		}); err != nil {
			return err
		}
		read, err := wsReadObject(ctx, conn)
		if err != nil {
			return err
		}
		return wsWriteObject(ctx, conn, map[string]any{
			"id": read["id"], "result": map[string]any{"thread": observerThread(
				rootID, "session", "", "/repo", "active", "waitingOnUserInput",
			)},
		})
	})

	observer, err := NewObserver(ObserverConfig{
		SocketPath: server.socketPath, ClientVersion: observerTestVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := observer.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	snapshot := session.Snapshot()
	if len(snapshot) != 1 || !reflect.DeepEqual(
		snapshot[0].Status.ActiveFlags, []ActiveFlag{ActiveWaitingOnUserInput},
	) {
		t.Fatalf("snapshot = %#v, want preexisting waiting status from thread/read", snapshot)
	}
}

func TestObserverPreResponseStatusConflictIsConservativelyUnknown(t *testing.T) {
	tests := []struct {
		name              string
		notificationNewer bool
	}{
		{name: "notification newer than sampled read", notificationNewer: true},
		{name: "notification older than sampled read"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const rootID = "11111111-1111-4111-8111-111111111111"
			server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
				if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
					return err
				}
				list, err := wsReadObject(ctx, conn)
				if err != nil {
					return err
				}
				if err := wsWriteObject(ctx, conn, map[string]any{
					"id": list["id"], "result": map[string]any{"data": []string{rootID}, "nextCursor": nil},
				}); err != nil {
					return err
				}
				read, err := wsReadObject(ctx, conn)
				if err != nil {
					return err
				}

				var sampledRead map[string]any
				if test.notificationNewer {
					// Upstream sampled idle first, then published the newer active
					// notification before sending the older response.
					sampledRead = observerThread(rootID, "session", "", "/repo", "idle")
				}
				if err := wsWriteObject(ctx, conn, map[string]any{
					"method": "thread/status/changed", "params": map[string]any{
						"threadId": rootID,
						"status": map[string]any{
							"type": "active", "activeFlags": []string{"waitingOnUserInput"},
						},
					},
				}); err != nil {
					return err
				}
				if !test.notificationNewer {
					// Upstream published active first, then sampled the newer idle read;
					// its wire sequence is indistinguishable from the case above.
					sampledRead = observerThread(rootID, "session", "", "/repo", "idle")
				}
				return wsWriteObject(ctx, conn, map[string]any{
					"id": read["id"], "result": map[string]any{"thread": sampledRead},
				})
			})

			observer, err := NewObserver(ObserverConfig{
				SocketPath: server.socketPath, ClientVersion: observerTestVersion,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			session, err := observer.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			snapshot := session.Snapshot()
			if len(snapshot) != 1 || snapshot[0].Status.Type != ThreadStatusNotLoaded {
				t.Fatalf("snapshot = %#v, want one thread with conservative notLoaded status", snapshot)
			}
		})
	}
}

func TestObserverStatusBeforeTargetReadWindowIsSuperseded(t *testing.T) {
	server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
		if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
			return err
		}
		list, err := wsReadObject(ctx, conn)
		if err != nil {
			return err
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": list["id"], "result": map[string]any{
				"data": []string{"root", "child"}, "nextCursor": nil,
			},
		}); err != nil {
			return err
		}

		rootRead, err := wsReadObject(ctx, conn)
		if err != nil {
			return err
		}
		// This child update is received while root/read is outstanding, before
		// child/read is even sent. The later child read is causally authoritative.
		if err := wsWriteObject(ctx, conn, map[string]any{
			"method": "thread/status/changed", "params": map[string]any{
				"threadId": "child", "status": map[string]any{
					"type": "active", "activeFlags": []string{"waitingOnUserInput"},
				},
			},
		}); err != nil {
			return err
		}
		if err := wsWriteObject(ctx, conn, map[string]any{
			"id": rootRead["id"], "result": map[string]any{
				"thread": observerThread("root", "session", "", "/repo", "idle"),
			},
		}); err != nil {
			return err
		}

		childRead, err := wsReadObject(ctx, conn)
		if err != nil {
			return err
		}
		return wsWriteObject(ctx, conn, map[string]any{
			"id": childRead["id"], "result": map[string]any{
				"thread": observerThread("child", "session", "root", "/repo", "idle"),
			},
		})
	})

	observer, err := NewObserver(ObserverConfig{
		SocketPath: server.socketPath, ClientVersion: observerTestVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := observer.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	for _, thread := range session.Snapshot() {
		if thread.ID == "child" {
			if thread.Status.Type != ThreadStatusIdle {
				t.Fatalf("child status = %#v, want later read's idle status", thread.Status)
			}
			return
		}
	}
	t.Fatal("child missing from snapshot")
}

func TestReplaySnapshotJournalReconcilesReadStatusWithoutInventingOrder(t *testing.T) {
	root := Thread{ID: "root", SessionID: "session", CWD: "/repo", Status: idleStatus()}
	active := statusEvent("root", activeStatus(ActiveWaitingOnUserInput))
	idle := statusEvent("root", idleStatus())
	read := journalEntry{event: threadEvent(root), readUpsert: true}
	startedActive := root
	startedActive.Status = activeStatus(ActiveWaitingOnUserInput)
	activeReadThread := root
	activeReadThread.Status = activeStatus(ActiveWaitingOnApproval, ActiveWaitingOnUserInput)
	activeRead := journalEntry{event: threadEvent(activeReadThread), readUpsert: true}
	reorderedActive := statusEvent(
		"root", activeStatus(ActiveWaitingOnUserInput, ActiveWaitingOnApproval),
	)
	duringRootRead := func(event ReducerEvent) journalEntry {
		return journalEntry{event: event, outstandingReadThreadID: "root"}
	}
	loaded := map[string]struct{}{"root": {}}

	tests := []struct {
		name    string
		journal []journalEntry
		want    ThreadStatus
	}{
		{
			name:    "newer notification arrives before older response",
			journal: []journalEntry{duringRootRead(active), read},
			want:    ThreadStatus{Type: ThreadStatusNotLoaded},
		},
		{
			name:    "older notification arrives before newer response",
			journal: []journalEntry{duringRootRead(active), read},
			want:    ThreadStatus{Type: ThreadStatusNotLoaded},
		},
		{
			name:    "pre-read thread started status also conflicts conservatively",
			journal: []journalEntry{duringRootRead(threadEvent(startedActive)), read},
			want:    ThreadStatus{Type: ThreadStatusNotLoaded},
		},
		{
			name: "any differing pre-read sample remains unknown",
			journal: []journalEntry{
				duringRootRead(active), duringRootRead(idle), read,
			},
			want: ThreadStatus{Type: ThreadStatusNotLoaded},
		},
		{
			name:    "same value before response is idempotent",
			journal: []journalEntry{duringRootRead(idle), read},
			want:    idleStatus(),
		},
		{
			name:    "active flag order is semantically equal",
			journal: []journalEntry{duringRootRead(reorderedActive), activeRead},
			want:    activeReadThread.Status,
		},
		{
			name:    "status before target read window is superseded",
			journal: []journalEntry{{event: active}, read},
			want:    idleStatus(),
		},
		{
			name:    "thread started before target read window is superseded",
			journal: []journalEntry{{event: threadEvent(startedActive)}, read},
			want:    idleStatus(),
		},
		{
			name:    "notification after response is authoritative",
			journal: []journalEntry{read, {event: active}},
			want:    activeStatus(ActiveWaitingOnUserInput),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			threads, err := replaySnapshotJournal(test.journal, loaded)
			if err != nil {
				t.Fatal(err)
			}
			if got := threads["root"].Status; !reflect.DeepEqual(got, test.want) {
				t.Fatalf("status = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReplaySnapshotJournalCloseOrderingNeverResurrectsThread(t *testing.T) {
	root := Thread{ID: "root", SessionID: "session", CWD: "/repo", Status: idleStatus()}
	read := journalEntry{event: threadEvent(root), readUpsert: true}
	closed := ReducerEvent{Kind: EventThreadClosed, ThreadID: "root"}
	loaded := map[string]struct{}{"root": {}}

	t.Run("close before response rejects stale read", func(t *testing.T) {
		_, err := replaySnapshotJournal([]journalEntry{
			{event: closed, outstandingReadThreadID: "root"}, read,
		}, loaded)
		if err == nil {
			t.Fatal("replay accepted a read upsert after thread close")
		}
	})

	t.Run("close after response removes thread", func(t *testing.T) {
		threads, err := replaySnapshotJournal([]journalEntry{read, {event: closed}}, loaded)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := threads["root"]; exists {
			t.Fatal("thread survived close after read upsert")
		}
	})
}

func TestObserverRejectsMalformedKnownMessagesAndRepeatedPagination(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *websocket.Conn) error
	}{
		{
			name: "binary message",
			run: func(ctx context.Context, conn *websocket.Conn) error {
				if _, err := wsReadObject(ctx, conn); err != nil {
					return err
				}
				return conn.Write(ctx, websocket.MessageBinary, []byte(`{"id":"wisp-1","result":{}}`))
			},
		},
		{
			name: "trailing JSON",
			run: func(ctx context.Context, conn *websocket.Conn) error {
				if _, err := wsReadObject(ctx, conn); err != nil {
					return err
				}
				return conn.Write(ctx, websocket.MessageText, []byte(`{"id":"wisp-1","result":{}} {}`))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startUDSWSServer(t, test.run)
			observer, err := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if session, err := observer.Open(ctx); err == nil {
				session.Close()
				t.Fatal("Open accepted malformed protocol")
			}
		})
	}
}

func TestObserverRejectsRepeatedCursorAndDuplicateLoadedID(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *websocket.Conn) error
	}{
		{
			name: "repeated cursor",
			run: func(ctx context.Context, conn *websocket.Conn) error {
				if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
					return err
				}
				first, err := wsReadObject(ctx, conn)
				if err != nil {
					return err
				}
				if err := wsWriteObject(ctx, conn, map[string]any{"id": first["id"], "result": map[string]any{"data": []string{}, "nextCursor": "same"}}); err != nil {
					return err
				}
				second, err := wsReadObject(ctx, conn)
				if err != nil {
					return err
				}
				return wsWriteObject(ctx, conn, map[string]any{"id": second["id"], "result": map[string]any{"data": []string{}, "nextCursor": "same"}})
			},
		},
		{
			name: "duplicate loaded ID",
			run: func(ctx context.Context, conn *websocket.Conn) error {
				if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
					return err
				}
				request, err := wsReadObject(ctx, conn)
				if err != nil {
					return err
				}
				return wsWriteObject(ctx, conn, map[string]any{"id": request["id"], "result": map[string]any{"data": []string{"same", "same"}, "nextCursor": nil}})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startUDSWSServer(t, test.run)
			observer, err := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if session, err := observer.Open(ctx); err == nil {
				session.Close()
				t.Fatal("Open accepted ambiguous pagination")
			}
		})
	}
}

func TestObserverCapsUniqueEmptyPagination(t *testing.T) {
	server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
		if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
			return err
		}
		for page := 0; page <= MaxObserverThreads; page++ {
			request, err := wsReadObject(ctx, conn)
			if err != nil {
				return nil // bounded client closed before requesting another page
			}
			if err := wsWriteObject(ctx, conn, map[string]any{
				"id": request["id"], "result": map[string]any{"data": []string{}, "nextCursor": fmt.Sprintf("cursor-%d", page)},
			}); err != nil {
				return nil
			}
		}
		// An unbounded client requests page 4098; give it a terminal page so
		// Open returns success and the regression is observable.
		request, err := wsReadObject(ctx, conn)
		if err != nil {
			return nil
		}
		return wsWriteObject(ctx, conn, map[string]any{"id": request["id"], "result": map[string]any{"data": []string{}, "nextCursor": nil}})
	})
	observer, err := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if session, err := observer.Open(ctx); err == nil {
		session.Close()
		t.Fatal("Open accepted unbounded unique empty pagination")
	}
}

func TestObserverReadLimitAcceptsExactlyOneMiBAndRejectsOneByteMore(t *testing.T) {
	for _, test := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "exact limit", size: MaxObserverMessageBytes},
		{name: "one byte over", size: MaxObserverMessageBytes + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
				if _, err := wsReadObject(ctx, conn); err != nil {
					return err
				}
				payload := initializeFrameOfSize(t, test.size)
				if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
					return nil
				}
				if test.wantErr {
					return nil
				}
				if _, err := wsReadObject(ctx, conn); err != nil { // initialized
					return err
				}
				list, err := wsReadObject(ctx, conn)
				if err != nil {
					return err
				}
				return wsWriteObject(ctx, conn, map[string]any{"id": list["id"], "result": map[string]any{"data": []string{}, "nextCursor": nil}})
			})
			observer, err := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			session, openErr := observer.Open(ctx)
			if session != nil {
				session.Close()
			}
			if test.wantErr != (openErr != nil) {
				t.Fatalf("Open error = %v, wantErr=%v", openErr, test.wantErr)
			}
		})
	}
}

func TestObserverRejectsInvalidInitializeMixedEnvelopeAndMalformedKnownNotifications(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *websocket.Conn) error
	}{
		{
			name: "missing initialize platform field",
			run: func(ctx context.Context, conn *websocket.Conn) error {
				if _, err := wsReadObject(ctx, conn); err != nil {
					return err
				}
				return wsWriteObject(ctx, conn, map[string]any{"id": "wisp-1", "result": map[string]any{"userAgent": "u", "codexHome": "/", "platformFamily": "unix"}})
			},
		},
		{
			name: "mixed request and response envelope",
			run: func(ctx context.Context, conn *websocket.Conn) error {
				if _, err := wsReadObject(ctx, conn); err != nil {
					return err
				}
				if err := wsWriteObject(ctx, conn, map[string]any{
					"id": "wisp-1", "method": "thread/started", "params": map[string]any{}, "result": validInitializeResult(),
				}); err != nil {
					return err
				}
				// If the malformed mixed shape is silently ignored, complete a valid
				// empty snapshot so Open incorrectly succeeds.
				if err := wsWriteObject(ctx, conn, map[string]any{"id": "wisp-1", "result": validInitializeResult()}); err != nil {
					return err
				}
				if _, err := wsReadObject(ctx, conn); err != nil {
					return nil
				}
				list, err := wsReadObject(ctx, conn)
				if err != nil {
					return nil
				}
				return wsWriteObject(ctx, conn, map[string]any{"id": list["id"], "result": map[string]any{"data": []string{}, "nextCursor": nil}})
			},
		},
		{
			name: "malformed known notification before initialize",
			run: func(ctx context.Context, conn *websocket.Conn) error {
				if _, err := wsReadObject(ctx, conn); err != nil {
					return err
				}
				return wsWriteObject(ctx, conn, map[string]any{
					"method": "thread/status/changed", "params": map[string]any{"threadId": "root", "status": map[string]any{"type": "active"}},
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startUDSWSServer(t, test.run)
			observer, err := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if session, err := observer.Open(ctx); err == nil {
				session.Close()
				t.Fatal("Open accepted malformed known protocol")
			}
		})
	}
}

func TestObserverRejectsUnknownStatusDuringSnapshotAndLive(t *testing.T) {
	t.Run("snapshot", func(t *testing.T) {
		server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
			if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
				return err
			}
			list, err := wsReadObject(ctx, conn)
			if err != nil {
				return err
			}
			if err := wsWriteObject(ctx, conn, map[string]any{
				"method": "thread/status/changed", "params": map[string]any{"threadId": "ghost", "status": map[string]any{"type": "idle"}},
			}); err != nil {
				return err
			}
			return wsWriteObject(ctx, conn, map[string]any{"id": list["id"], "result": map[string]any{"data": []string{}, "nextCursor": nil}})
		})
		observer, _ := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if session, err := observer.Open(ctx); err == nil {
			session.Close()
			t.Fatal("snapshot accepted status-only unknown thread")
		}
	})

	t.Run("live", func(t *testing.T) {
		server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
			if err := serveEmptyObserverSnapshot(ctx, conn); err != nil {
				return err
			}
			return wsWriteObject(ctx, conn, map[string]any{
				"method": "thread/status/changed", "params": map[string]any{"threadId": "ghost", "status": map[string]any{"type": "idle"}},
			})
		})
		observer, _ := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		session, err := observer.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err := session.Next(ctx); err == nil {
			t.Fatal("live stream accepted status-only unknown thread")
		}
	})
}

func TestObserverRejectsEmptyParentAndEveryControlCharacter(t *testing.T) {
	t.Run("empty parent", func(t *testing.T) {
		server := startUDSWSServer(t, func(ctx context.Context, conn *websocket.Conn) error {
			if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
				return err
			}
			list, err := wsReadObject(ctx, conn)
			if err != nil {
				return err
			}
			if err := wsWriteObject(ctx, conn, map[string]any{"id": list["id"], "result": map[string]any{"data": []string{"thread"}, "nextCursor": nil}}); err != nil {
				return err
			}
			read, err := wsReadObject(ctx, conn)
			if err != nil {
				return err
			}
			thread := observerThread("thread", "session", "", "/repo", "idle")
			thread["parentThreadId"] = ""
			return wsWriteObject(ctx, conn, map[string]any{"id": read["id"], "result": map[string]any{"thread": thread}})
		})
		observer, _ := NewObserver(ObserverConfig{SocketPath: server.socketPath, ClientVersion: observerTestVersion})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if session, err := observer.Open(ctx); err == nil {
			session.Close()
			t.Fatal("empty parentThreadId was coerced to top-level")
		}
	})

	for value := byte(0); value < 0x20; value++ {
		value := value
		t.Run(fmt.Sprintf("client version C0 %02x", value), func(t *testing.T) {
			if _, err := NewObserver(ObserverConfig{SocketPath: "/tmp/a.sock", ClientVersion: "v" + string([]byte{value})}); err == nil {
				t.Fatalf("control byte 0x%02x accepted", value)
			}
		})
	}
	t.Run("client version DEL", func(t *testing.T) {
		if _, err := NewObserver(ObserverConfig{SocketPath: "/tmp/a.sock", ClientVersion: "v\x7f"}); err == nil {
			t.Fatal("DEL accepted")
		}
	})
}

func TestObserverOpenHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	observer, err := NewObserver(ObserverConfig{SocketPath: filepath.Join(dir, "missing.sock"), ClientVersion: observerTestVersion})
	if err != nil {
		t.Fatal(err)
	}
	if session, err := observer.Open(ctx); err == nil || session != nil {
		t.Fatalf("Open() = (%#v, %v), want cancellation", session, err)
	}
}

func threadIDs(threads []Thread) []string {
	ids := make([]string, len(threads))
	for i := range threads {
		ids[i] = threads[i].ID
	}
	return ids
}

func validInitializeResult() map[string]any {
	return map[string]any{"userAgent": "codex-test", "codexHome": "/tmp/codex", "platformFamily": "unix", "platformOs": "macos"}
}

func serveObserverInitialize(ctx context.Context, conn *websocket.Conn, result map[string]any) error {
	if _, err := wsReadObject(ctx, conn); err != nil {
		return err
	}
	if err := wsWriteObject(ctx, conn, map[string]any{"id": "wisp-1", "result": result}); err != nil {
		return err
	}
	_, err := wsReadObject(ctx, conn) // initialized
	return err
}

func serveEmptyObserverSnapshot(ctx context.Context, conn *websocket.Conn) error {
	if err := serveObserverInitialize(ctx, conn, validInitializeResult()); err != nil {
		return err
	}
	list, err := wsReadObject(ctx, conn)
	if err != nil {
		return err
	}
	return wsWriteObject(ctx, conn, map[string]any{"id": list["id"], "result": map[string]any{"data": []string{}, "nextCursor": nil}})
}

func initializeFrameOfSize(t *testing.T, size int) []byte {
	t.Helper()
	prefix := []byte(`{"id":"wisp-1","result":{"userAgent":"u","codexHome":"/","platformFamily":"unix","platformOs":"macos","padding":"`)
	suffix := []byte(`"}}`)
	if size < len(prefix)+len(suffix) {
		t.Fatalf("frame size %d is too small", size)
	}
	return bytes.Join([][]byte{prefix, bytes.Repeat([]byte{'x'}, size-len(prefix)-len(suffix)), suffix}, nil)
}
