package opencodeadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"time"
)

const snapshotTimeout = 3 * time.Second

type snapshotSession struct {
	ID       string          `json:"id"`
	ParentID json.RawMessage `json:"parentID"`
}

func loadInitialSnapshot(ctx context.Context, client *http.Client, baseURL, directory, password string) ([]Event, bool, error) {
	snapshotCtx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()

	var sessions []snapshotSession
	if err := getSnapshotJSON(snapshotCtx, client, baseURL, "/session", directory, password, &sessions); err != nil {
		return nil, false, err
	}
	var statuses map[string]json.RawMessage
	if err := getSnapshotJSON(snapshotCtx, client, baseURL, "/session/status", directory, password, &statuses); err != nil {
		return nil, false, err
	}
	var questions []json.RawMessage
	if err := getSnapshotJSON(snapshotCtx, client, baseURL, "/question", directory, password, &questions); err != nil {
		return nil, false, err
	}
	var permissions []json.RawMessage
	if err := getSnapshotJSON(snapshotCtx, client, baseURL, "/permission", directory, password, &permissions); err != nil {
		return nil, false, err
	}
	if len(sessions) > MaxModelEntries || len(statuses) > MaxModelEntries ||
		len(questions) > MaxModelEntries || len(permissions) > MaxModelEntries {
		return nil, false, errors.New("OpenCode snapshot exceeds model bounds")
	}

	events := make([]Event, 0, len(sessions)+len(statuses)+len(questions)+len(permissions))
	seenSessions := make(map[string]bool, len(sessions))
	roots := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if !identifier(session.ID) || seenSessions[session.ID] {
			return nil, false, errors.New("OpenCode snapshot contains an invalid or duplicate session")
		}
		seenSessions[session.ID] = true
		event := Event{Kind: EventSessionUpsert, SessionID: session.ID}
		if len(session.ParentID) == 0 {
			roots = append(roots, session.ID)
		} else {
			var parentID string
			if json.Unmarshal(session.ParentID, &parentID) != nil || !identifier(parentID) {
				return nil, false, errors.New("OpenCode snapshot contains an invalid parent session")
			}
			event.ParentID, event.HasParent = parentID, true
		}
		events = append(events, event)
	}

	statusIDs := make([]string, 0, len(statuses))
	for sessionID := range statuses {
		statusIDs = append(statusIDs, sessionID)
	}
	sort.Strings(statusIDs)
	for _, root := range roots {
		if _, exists := statuses[root]; !exists {
			events = append(events, Event{Kind: EventSessionStatus, SessionID: root, Status: "idle"})
		}
	}
	for _, sessionID := range statusIDs {
		if !identifier(sessionID) || !seenSessions[sessionID] {
			return nil, false, errors.New("OpenCode snapshot status references an unknown session")
		}
		event, err := decodeSnapshotEvent("session.status", struct {
			SessionID string          `json:"sessionID"`
			Status    json.RawMessage `json:"status"`
		}{SessionID: sessionID, Status: statuses[sessionID]})
		if err != nil {
			return nil, false, fmt.Errorf("decode OpenCode status snapshot: %w", err)
		}
		events = append(events, event)
	}
	for _, values := range []struct {
		kind string
		raw  []json.RawMessage
	}{{kind: "question.asked", raw: questions}, {kind: "permission.asked", raw: permissions}} {
		for _, raw := range values.raw {
			event, err := decodeSnapshotEvent(values.kind, raw)
			if err != nil {
				return nil, false, fmt.Errorf("decode OpenCode %s snapshot: %w", values.kind, err)
			}
			if !seenSessions[event.SessionID] {
				return nil, false, fmt.Errorf("OpenCode %s snapshot references an unknown session", values.kind)
			}
			events = append(events, event)
		}
	}
	return events, len(sessions)+len(statuses)+len(questions)+len(permissions) > 0, nil
}

func getSnapshotJSON(ctx context.Context, client *http.Client, baseURL, path, directory, password string, target any) error {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	endpoint.Path = path
	query := endpoint.Query()
	if directory != "" {
		query.Set("directory", directory)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create OpenCode snapshot request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.SetBasicAuth("opencode", password)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request OpenCode snapshot %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenCode snapshot %s returned status %d", path, response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("OpenCode snapshot %s content type %q is not application/json", path, response.Header.Get("Content-Type"))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxEventBytes+1))
	if err != nil {
		return fmt.Errorf("read OpenCode snapshot %s: %w", path, err)
	}
	if len(data) == 0 || len(data) > MaxEventBytes {
		return fmt.Errorf("OpenCode snapshot %s size is outside 1..%d bytes", path, MaxEventBytes)
	}
	if err := decodeOne(data, target); err != nil {
		return fmt.Errorf("decode OpenCode snapshot %s: %w", path, err)
	}
	return nil
}

func decodeSnapshotEvent(kind string, properties any) (Event, error) {
	raw, ok := properties.(json.RawMessage)
	if !ok {
		encoded, err := json.Marshal(properties)
		if err != nil {
			return Event{}, err
		}
		raw = encoded
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return Event{}, errors.New("snapshot entry is empty")
	}
	payload, err := json.Marshal(eventEnvelope{Type: kind, Properties: raw})
	if err != nil {
		return Event{}, err
	}
	return DecodeEvent(payload)
}
