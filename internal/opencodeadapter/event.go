package opencodeadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxEventBytes      = 1024 * 1024
	MaxIdentifierBytes = 256
	MaxSchemaBytes     = 64 * 1024
	MaxNestedItems     = 256
)

type EventKind uint8

const (
	EventIgnored EventKind = iota
	EventSessionUpsert
	EventSessionDeleted
	EventSessionStatus
	EventSessionError
	EventQuestionAsked
	EventQuestionCleared
	EventPermissionAsked
	EventPermissionCleared
)

type Event struct {
	Kind       EventKind
	ID         string
	SessionID  string
	ParentID   string
	HasParent  bool
	RequestID  string
	Status     string
	Reply      string
	Question   bool
	Permission bool
}

type eventEnvelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

func DecodeEvent(data []byte) (Event, error) {
	if len(data) == 0 || len(data) > MaxEventBytes {
		return Event{}, fmt.Errorf("OpenCode event size is outside 1..%d bytes", MaxEventBytes)
	}
	var envelope eventEnvelope
	if err := decodeOne(data, &envelope); err != nil {
		return Event{}, fmt.Errorf("decode OpenCode event: %w", err)
	}
	if !boundedString(envelope.Type, MaxIdentifierBytes) || envelope.Type == "" {
		return Event{}, errors.New("OpenCode event type is empty or oversized")
	}
	switch envelope.Type {
	case "session.created", "session.updated", "session.deleted":
		var properties struct {
			SessionID string `json:"sessionID"`
			Info      struct {
				ID       string          `json:"id"`
				ParentID json.RawMessage `json:"parentID"`
			} `json:"info"`
		}
		if err := decodeProperties(envelope.Properties, &properties); err != nil {
			return Event{}, err
		}
		if !identifier(properties.SessionID) || properties.SessionID != properties.Info.ID {
			return Event{}, errors.New("OpenCode session event has inconsistent identifiers")
		}
		event := Event{Kind: EventSessionUpsert, SessionID: properties.SessionID}
		if len(properties.Info.ParentID) > 0 {
			var parentID string
			if json.Unmarshal(properties.Info.ParentID, &parentID) != nil || !identifier(parentID) {
				return Event{}, errors.New("OpenCode session parent identifier is invalid")
			}
			event.ParentID, event.HasParent = parentID, true
		}
		if envelope.Type == "session.deleted" {
			event.Kind = EventSessionDeleted
		}
		return event, nil

	case "session.status":
		var properties struct {
			SessionID string          `json:"sessionID"`
			Status    json.RawMessage `json:"status"`
		}
		if err := decodeProperties(envelope.Properties, &properties); err != nil || !identifier(properties.SessionID) {
			return Event{}, errors.New("OpenCode session status has an invalid session identifier")
		}
		var status struct {
			Type    string          `json:"type"`
			Attempt *int64          `json:"attempt"`
			Message *string         `json:"message"`
			Next    *int64          `json:"next"`
			Action  json.RawMessage `json:"action"`
		}
		if err := decodeProperties(properties.Status, &status); err != nil {
			return Event{}, err
		}
		switch status.Type {
		case "idle", "busy":
		case "retry":
			if status.Attempt == nil || *status.Attempt < 0 || status.Message == nil || !boundedString(*status.Message, MaxSchemaBytes) || status.Next == nil || *status.Next < 0 {
				return Event{}, errors.New("OpenCode retry status is malformed")
			}
			if len(status.Action) > 0 {
				if !jsonObject(status.Action) {
					return Event{}, errors.New("OpenCode retry action is malformed")
				}
				var action struct {
					Reason   *string `json:"reason"`
					Provider *string `json:"provider"`
					Title    *string `json:"title"`
					Message  *string `json:"message"`
					Label    *string `json:"label"`
					Link     *string `json:"link"`
				}
				if err := decodeProperties(status.Action, &action); err != nil ||
					action.Reason == nil || action.Provider == nil || action.Title == nil || action.Message == nil || action.Label == nil ||
					!boundedString(*action.Reason, MaxSchemaBytes) || !boundedString(*action.Provider, MaxSchemaBytes) ||
					!boundedString(*action.Title, MaxSchemaBytes) || !boundedString(*action.Message, MaxSchemaBytes) ||
					!boundedString(*action.Label, MaxSchemaBytes) ||
					(action.Link != nil && !boundedString(*action.Link, MaxSchemaBytes)) {
					return Event{}, errors.New("OpenCode retry action is malformed")
				}
			}
		default:
			return Event{}, fmt.Errorf("unsupported OpenCode session status %q", status.Type)
		}
		return Event{Kind: EventSessionStatus, SessionID: properties.SessionID, Status: status.Type}, nil

	case "session.error":
		if !identifier(envelope.ID) {
			return Event{}, errors.New("OpenCode error event identifier is invalid")
		}
		var properties struct {
			SessionID string          `json:"sessionID"`
			Error     json.RawMessage `json:"error"`
		}
		if err := decodeProperties(envelope.Properties, &properties); err != nil || !identifier(properties.SessionID) {
			return Event{}, errors.New("OpenCode error session identifier is invalid")
		}
		if err := validateSessionError(properties.Error); err != nil {
			return Event{}, err
		}
		return Event{Kind: EventSessionError, ID: envelope.ID, SessionID: properties.SessionID}, nil

	case "question.asked":
		var properties struct {
			ID        string          `json:"id"`
			SessionID string          `json:"sessionID"`
			Questions json.RawMessage `json:"questions"`
			Tool      json.RawMessage `json:"tool"`
		}
		if err := decodeProperties(envelope.Properties, &properties); err != nil || !identifier(properties.ID) || !identifier(properties.SessionID) {
			return Event{}, errors.New("OpenCode question request is malformed")
		}
		var questions []struct {
			Header   *string         `json:"header"`
			Question *string         `json:"question"`
			Options  json.RawMessage `json:"options"`
			Multiple json.RawMessage `json:"multiple"`
			Custom   json.RawMessage `json:"custom"`
		}
		if !jsonArray(properties.Questions) || decodeOne(properties.Questions, &questions) != nil || len(questions) > MaxNestedItems || !validOptionalTool(properties.Tool) {
			return Event{}, errors.New("OpenCode question request is malformed")
		}
		for _, question := range questions {
			var options []struct {
				Label       *string `json:"label"`
				Description *string `json:"description"`
			}
			if question.Header == nil || question.Question == nil || !boundedString(*question.Header, MaxSchemaBytes) || !boundedString(*question.Question, MaxSchemaBytes) || !jsonArray(question.Options) || decodeOne(question.Options, &options) != nil || len(options) > MaxNestedItems || !validOptionalBool(question.Multiple) || !validOptionalBool(question.Custom) {
				return Event{}, errors.New("OpenCode question schema exceeds its bounds")
			}
			for _, option := range options {
				if option.Label == nil || option.Description == nil || !boundedString(*option.Label, MaxSchemaBytes) || !boundedString(*option.Description, MaxSchemaBytes) {
					return Event{}, errors.New("OpenCode question option exceeds its bounds")
				}
			}
		}
		return Event{Kind: EventQuestionAsked, RequestID: properties.ID, SessionID: properties.SessionID, Question: true}, nil

	case "question.replied", "question.rejected":
		var properties struct {
			RequestID string          `json:"requestID"`
			SessionID string          `json:"sessionID"`
			Answers   json.RawMessage `json:"answers"`
		}
		if err := decodeProperties(envelope.Properties, &properties); err != nil || !identifier(properties.RequestID) || !identifier(properties.SessionID) {
			return Event{}, errors.New("OpenCode question reply is malformed")
		}
		if envelope.Type == "question.replied" {
			var answers [][]string
			if !jsonArray(properties.Answers) || decodeOne(properties.Answers, &answers) != nil || len(answers) > MaxNestedItems {
				return Event{}, errors.New("OpenCode question answers exceed their bound")
			}
			for _, answer := range answers {
				if len(answer) > MaxNestedItems {
					return Event{}, errors.New("OpenCode question answer exceeds its bound")
				}
				for _, item := range answer {
					if !boundedString(item, MaxSchemaBytes) {
						return Event{}, errors.New("OpenCode question answer string is oversized")
					}
				}
			}
		}
		return Event{Kind: EventQuestionCleared, RequestID: properties.RequestID, SessionID: properties.SessionID, Question: true}, nil

	case "permission.asked":
		var properties struct {
			ID         string          `json:"id"`
			SessionID  string          `json:"sessionID"`
			Permission *string         `json:"permission"`
			Patterns   json.RawMessage `json:"patterns"`
			Metadata   json.RawMessage `json:"metadata"`
			Always     json.RawMessage `json:"always"`
			Tool       json.RawMessage `json:"tool"`
		}
		if err := decodeProperties(envelope.Properties, &properties); err != nil || !identifier(properties.ID) || !identifier(properties.SessionID) || properties.Permission == nil || !boundedString(*properties.Permission, MaxSchemaBytes) || !jsonArray(properties.Patterns) || !jsonObject(properties.Metadata) || !jsonArray(properties.Always) || !validOptionalTool(properties.Tool) {
			return Event{}, errors.New("OpenCode permission request is malformed")
		}
		var patterns, always []string
		var metadata map[string]json.RawMessage
		if decodeOne(properties.Patterns, &patterns) != nil || decodeOne(properties.Always, &always) != nil || decodeOne(properties.Metadata, &metadata) != nil || len(patterns) > MaxNestedItems || len(metadata) > MaxNestedItems || len(always) > MaxNestedItems {
			return Event{}, errors.New("OpenCode permission request is malformed")
		}
		for _, values := range [][]string{patterns, always} {
			for _, value := range values {
				if !boundedString(value, MaxSchemaBytes) {
					return Event{}, errors.New("OpenCode permission value is oversized")
				}
			}
		}
		for key := range metadata {
			if !boundedString(key, MaxSchemaBytes) {
				return Event{}, errors.New("OpenCode permission metadata key is oversized")
			}
		}
		return Event{Kind: EventPermissionAsked, RequestID: properties.ID, SessionID: properties.SessionID, Permission: true}, nil

	case "permission.replied":
		var properties struct {
			RequestID string `json:"requestID"`
			SessionID string `json:"sessionID"`
			Reply     string `json:"reply"`
		}
		if err := decodeProperties(envelope.Properties, &properties); err != nil || !identifier(properties.RequestID) || !identifier(properties.SessionID) || (properties.Reply != "once" && properties.Reply != "always" && properties.Reply != "reject") {
			return Event{}, errors.New("OpenCode permission reply is malformed")
		}
		return Event{Kind: EventPermissionCleared, RequestID: properties.RequestID, SessionID: properties.SessionID, Reply: properties.Reply, Permission: true}, nil
	default:
		return Event{Kind: EventIgnored}, nil
	}
}

func decodeProperties(data []byte, target any) error {
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return errors.New("OpenCode event properties are missing")
	}
	return decodeOne(data, target)
}

func decodeOne(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateSessionError(raw []byte) error {
	var value struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if err := decodeProperties(raw, &value); err != nil {
		return err
	}
	var data map[string]json.RawMessage
	if err := decodeProperties(value.Data, &data); err != nil {
		return errors.New("OpenCode error data is malformed")
	}
	requireString := func(name string) bool {
		var result string
		return json.Unmarshal(data[name], &result) == nil && boundedString(result, MaxSchemaBytes)
	}
	optionalString := func(name string) bool {
		raw, exists := data[name]
		return !exists || func() bool {
			var result string
			return json.Unmarshal(raw, &result) == nil && boundedString(result, MaxSchemaBytes)
		}()
	}
	optionalNonNegativeInteger := func(name string) bool {
		raw, exists := data[name]
		if !exists {
			return true
		}
		var result int64
		return json.Unmarshal(raw, &result) == nil && result >= 0
	}
	optionalStringRecord := func(name string) bool {
		raw, exists := data[name]
		if !exists {
			return true
		}
		var result map[string]string
		if json.Unmarshal(raw, &result) != nil || len(result) > MaxNestedItems {
			return false
		}
		for key, item := range result {
			if !boundedString(key, MaxSchemaBytes) || !boundedString(item, MaxSchemaBytes) {
				return false
			}
		}
		return true
	}
	switch value.Name {
	case "ProviderAuthError":
		if !requireString("providerID") || !identifier(mustJSONString(data["providerID"])) || !requireString("message") {
			return errors.New("OpenCode provider auth error is malformed")
		}
	case "UnknownError":
		if !requireString("message") || !optionalString("ref") {
			return errors.New("OpenCode unknown error is malformed")
		}
	case "MessageAbortedError", "ContentFilterError":
		if !requireString("message") {
			return errors.New("OpenCode error message is malformed")
		}
	case "StructuredOutputError":
		var retries int64
		if !requireString("message") || json.Unmarshal(data["retries"], &retries) != nil || retries < 0 {
			return errors.New("OpenCode structured output error is malformed")
		}
	case "ContextOverflowError":
		if !requireString("message") || !optionalString("responseBody") {
			return errors.New("OpenCode context overflow error is malformed")
		}
	case "APIError":
		var retryable bool
		if !requireString("message") || json.Unmarshal(data["isRetryable"], &retryable) != nil ||
			!optionalNonNegativeInteger("statusCode") || !optionalString("responseBody") ||
			!optionalStringRecord("responseHeaders") || !optionalStringRecord("metadata") {
			return errors.New("OpenCode API error is malformed")
		}
	case "MessageOutputLengthError":
	default:
		return fmt.Errorf("unsupported OpenCode error %q", value.Name)
	}
	return nil
}

func mustJSONString(raw json.RawMessage) string {
	var result string
	_ = json.Unmarshal(raw, &result)
	return result
}

func jsonArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']'
}

func jsonObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func validOptionalTool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	if !jsonObject(raw) {
		return false
	}
	var tool struct {
		MessageID string `json:"messageID"`
		CallID    string `json:"callID"`
	}
	return decodeOne(raw, &tool) == nil && identifier(tool.MessageID) && identifier(tool.CallID)
}

func validOptionalBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	trimmed := bytes.TrimSpace(raw)
	return bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false"))
}

func identifier(value string) bool {
	return value != "" && boundedString(value, MaxIdentifierBytes) && !strings.ContainsAny(value, "\x00\t\r\n")
}

func boundedString(value string, limit int) bool {
	return utf8.ValidString(value) && len(value) <= limit
}
