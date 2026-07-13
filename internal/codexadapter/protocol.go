package codexadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxObserverMessageBytes = 1 << 20
	MaxObserverThreads      = 4096
	MaxObserverJournal      = MaxObserverThreads * 2
	loadedListPageSize      = 256
)

const (
	methodInitialize       = "initialize"
	methodInitialized      = "initialized"
	methodThreadLoadedList = "thread/loaded/list"
	methodThreadRead       = "thread/read"

	methodThreadStarted       = "thread/started"
	methodThreadStatusChanged = "thread/status/changed"
	methodThreadClosed        = "thread/closed"
)

type wireEnvelope struct {
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
	JSONRPC json.RawMessage `json:"jsonrpc"`
}

func decodeWireEnvelope(payload []byte) (wireEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var envelope wireEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return wireEnvelope{}, fmt.Errorf("decode app-server message: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return wireEnvelope{}, errors.New("app-server message contains trailing JSON")
		}
		return wireEnvelope{}, fmt.Errorf("decode trailing app-server data: %w", err)
	}
	if envelope.JSONRPC != nil {
		return wireEnvelope{}, errors.New("unexpected jsonrpc field")
	}

	hasID := len(envelope.ID) != 0
	hasMethod := envelope.Method != ""
	hasParams := len(envelope.Params) != 0
	hasResult := len(envelope.Result) != 0
	hasError := len(envelope.Error) != 0

	switch {
	case hasID && !hasMethod:
		if _, err := rawIDString(envelope.ID); err != nil {
			return wireEnvelope{}, err
		}
		if hasParams || hasResult == hasError {
			return wireEnvelope{}, errors.New("malformed app-server response envelope")
		}
	case hasID && hasMethod:
		if _, err := decodeRequestID(envelope.ID); err != nil {
			return wireEnvelope{}, err
		}
		if hasResult || hasError {
			return wireEnvelope{}, errors.New("malformed app-server request envelope")
		}
	case !hasID && hasMethod:
		if hasResult || hasError {
			return wireEnvelope{}, errors.New("malformed app-server notification envelope")
		}
	default:
		return wireEnvelope{}, errors.New("app-server message is not a response, request, or notification")
	}
	if err := validateObserverString("app-server method", envelope.Method, MaxReducerIDBytes, !hasMethod); err != nil {
		return wireEnvelope{}, err
	}
	return envelope, nil
}

func rawIDString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New("missing response id")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", errors.New("response id is not a non-empty string")
	}
	return value, nil
}

type initializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

func decodeInitializeResult(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("initialize response is missing result")
	}
	var result initializeResult
	if err := decodeSingleJSON(raw, &result); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	if result.UserAgent == "" || result.CodexHome == "" || result.PlatformFamily == "" || result.PlatformOS == "" {
		return errors.New("initialize result is missing required platform identity")
	}
	return nil
}

func decodeSingleJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}

type loadedListResult struct {
	Data       []string        `json:"data"`
	NextCursor json.RawMessage `json:"nextCursor"`
}

func decodeLoadedListResult(raw json.RawMessage) ([]string, *string, error) {
	var result loadedListResult
	if err := decodeSingleJSON(raw, &result); err != nil {
		return nil, nil, fmt.Errorf("decode loaded-list result: %w", err)
	}
	if result.Data == nil {
		return nil, nil, errors.New("loaded-list result is missing data")
	}
	for _, id := range result.Data {
		if err := validateObserverString("loaded thread id", id, MaxReducerIDBytes, false); err != nil {
			return nil, nil, err
		}
	}
	trimmed := bytes.TrimSpace(result.NextCursor)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return result.Data, nil, nil
	}
	var cursor string
	if err := json.Unmarshal(trimmed, &cursor); err != nil || cursor == "" {
		return nil, nil, errors.New("nextCursor must be null or a non-empty string")
	}
	if err := validateObserverString("next cursor", cursor, MaxReducerIDBytes, false); err != nil {
		return nil, nil, err
	}
	return result.Data, &cursor, nil
}

func decodeThreadReadResult(raw json.RawMessage) (Thread, error) {
	var result struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := decodeSingleJSON(raw, &result); err != nil {
		return Thread{}, fmt.Errorf("decode thread/read result: %w", err)
	}
	if len(result.Thread) == 0 {
		return Thread{}, errors.New("thread/read result is missing thread")
	}
	return decodeThread(result.Thread)
}

func decodeThread(raw json.RawMessage) (Thread, error) {
	var value struct {
		ID             string          `json:"id"`
		SessionID      string          `json:"sessionId"`
		ParentThreadID json.RawMessage `json:"parentThreadId"`
		CWD            string          `json:"cwd"`
		Status         json.RawMessage `json:"status"`
	}
	if err := decodeSingleJSON(raw, &value); err != nil {
		return Thread{}, fmt.Errorf("decode thread: %w", err)
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"thread id", value.ID, MaxReducerIDBytes},
		{"session id", value.SessionID, MaxReducerIDBytes},
		{"thread cwd", value.CWD, MaxReducerCWDBytes},
	} {
		if err := validateObserverString(field.name, field.value, field.limit, false); err != nil {
			return Thread{}, err
		}
	}
	parent, err := decodeNullableString(value.ParentThreadID)
	if err != nil {
		return Thread{}, fmt.Errorf("parentThreadId: %w", err)
	}
	if parent != "" {
		if err := validateObserverString("parent thread id", parent, MaxReducerIDBytes, false); err != nil {
			return Thread{}, err
		}
	}
	status, err := decodeThreadStatus(value.Status)
	if err != nil {
		return Thread{}, err
	}
	return Thread{ID: value.ID, SessionID: value.SessionID, ParentThreadID: parent, CWD: value.CWD, Status: status}, nil
}

func decodeNullableString(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", errors.New("field is missing")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", errors.New("expected null or string")
	}
	if value == "" {
		return "", errors.New("string value is empty")
	}
	return value, nil
}

func decodeThreadStatus(raw json.RawMessage) (ThreadStatus, error) {
	var value struct {
		Type        ThreadStatusType `json:"type"`
		ActiveFlags json.RawMessage  `json:"activeFlags"`
	}
	if len(raw) == 0 || decodeSingleJSON(raw, &value) != nil {
		return ThreadStatus{}, errors.New("malformed thread status")
	}
	status := ThreadStatus{Type: value.Type}
	switch value.Type {
	case ThreadStatusNotLoaded, ThreadStatusIdle, ThreadStatusSystemError:
		if len(value.ActiveFlags) != 0 {
			return ThreadStatus{}, errors.New("activeFlags is valid only for active status")
		}
	case ThreadStatusActive:
		if len(value.ActiveFlags) == 0 {
			return ThreadStatus{}, errors.New("active status is missing activeFlags")
		}
		var flags []ActiveFlag
		if err := decodeSingleJSON(value.ActiveFlags, &flags); err != nil || flags == nil {
			return ThreadStatus{}, errors.New("malformed activeFlags")
		}
		seen := make(map[ActiveFlag]struct{}, len(flags))
		for _, flag := range flags {
			switch flag {
			case ActiveWaitingOnApproval, ActiveWaitingOnUserInput:
			default:
				return ThreadStatus{}, fmt.Errorf("unsupported active flag %q", flag)
			}
			if _, duplicate := seen[flag]; duplicate {
				return ThreadStatus{}, fmt.Errorf("duplicate active flag %q", flag)
			}
			seen[flag] = struct{}{}
		}
		status.ActiveFlags = flags
	default:
		return ThreadStatus{}, fmt.Errorf("unsupported thread status %q", value.Type)
	}
	return status, nil
}

func decodeNotification(envelope wireEnvelope) (ReducerEvent, bool, error) {
	if len(envelope.ID) != 0 {
		// Server-to-client requests are deliberately observed but never answered.
		return ReducerEvent{}, false, nil
	}
	switch envelope.Method {
	case methodThreadStarted:
		var params struct {
			Thread json.RawMessage `json:"thread"`
		}
		if err := decodeSingleJSON(envelope.Params, &params); err != nil || len(params.Thread) == 0 {
			return ReducerEvent{}, true, errors.New("malformed thread/started notification")
		}
		thread, err := decodeThread(params.Thread)
		if err != nil {
			return ReducerEvent{}, true, err
		}
		return ReducerEvent{Kind: EventThreadObserved, Thread: thread}, true, nil

	case methodThreadStatusChanged:
		var params struct {
			ThreadID string          `json:"threadId"`
			Status   json.RawMessage `json:"status"`
		}
		if err := decodeSingleJSON(envelope.Params, &params); err != nil {
			return ReducerEvent{}, true, errors.New("malformed thread/status/changed notification")
		}
		if err := validateObserverString("status thread id", params.ThreadID, MaxReducerIDBytes, false); err != nil {
			return ReducerEvent{}, true, err
		}
		status, err := decodeThreadStatus(params.Status)
		if err != nil {
			return ReducerEvent{}, true, err
		}
		return ReducerEvent{Kind: EventThreadStatus, ThreadID: params.ThreadID, Status: status}, true, nil

	case methodThreadClosed:
		var params struct {
			ThreadID string `json:"threadId"`
		}
		if err := decodeSingleJSON(envelope.Params, &params); err != nil {
			return ReducerEvent{}, true, errors.New("malformed thread/closed notification")
		}
		if err := validateObserverString("closed thread id", params.ThreadID, MaxReducerIDBytes, false); err != nil {
			return ReducerEvent{}, true, err
		}
		return ReducerEvent{Kind: EventThreadClosed, ThreadID: params.ThreadID}, true, nil
	default:
		return ReducerEvent{}, false, nil
	}
}

func validateObserverString(name, value string, limit int, emptyOK bool) error {
	if value == "" && !emptyOK {
		return fmt.Errorf("%s is empty", name)
	}
	if len(value) > limit {
		return fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	if !utf8.ValidString(value) || strings.IndexFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0 {
		return fmt.Errorf("%s contains invalid bytes", name)
	}
	return nil
}

func decodeRequestID(raw json.RawMessage) (RequestID, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return RequestID{}, errors.New("missing request id")
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return RequestID{}, err
		}
		if err := validateObserverString("request id", value, MaxReducerIDBytes, false); err != nil {
			return RequestID{}, err
		}
		return StringRequestID(value), nil
	}
	value, err := strconv.ParseInt(string(trimmed), 10, 64)
	if err != nil || strings.ContainsAny(string(trimmed), ".eE") {
		return RequestID{}, errors.New("request id must be a string or int64")
	}
	return NumberRequestID(value), nil
}
