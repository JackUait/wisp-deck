package opencodeadapter

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackuait/wisp-deck/internal/attention"
)

const (
	MaxSSEEventBytes = MaxEventBytes
	maxSSELineBytes  = MaxEventBytes + 16
)

type StatePublisher interface {
	Publish(attention.Phase, attention.Reason, string) error
}

type ObserverOptions struct {
	BaseURL   string
	Password  string
	Directory string
	Reducer   *Reducer
	Publisher StatePublisher
	OnReady   func()
}

func ObserveEvents(ctx context.Context, options ObserverOptions) error {
	if err := validateLoopbackURL(options.BaseURL); err != nil {
		return err
	}
	if options.Password == "" {
		return errors.New("OpenCode observer password is empty")
	}
	if options.Reducer == nil || options.Publisher == nil {
		return errors.New("OpenCode observer reducer and publisher are required")
	}
	base, _ := url.Parse(options.BaseURL)
	base.Path = "/event"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return fmt.Errorf("create OpenCode event request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.SetBasicAuth("opencode", options.Password)

	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout:  3 * time.Second,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 32 * 1024,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("OpenCode event redirect rejected")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("observe OpenCode events: %w", ctx.Err())
		}
		return fmt.Errorf("open OpenCode event stream: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenCode event stream returned status %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return fmt.Errorf("OpenCode event stream content type %q is not text/event-stream", response.Header.Get("Content-Type"))
	}
	snapshot, populated, err := loadInitialSnapshot(ctx, client, options.BaseURL, options.Directory, options.Password)
	if err != nil {
		state := options.Reducer.Invalidate()
		_ = options.Publisher.Publish(state.Phase, state.Reason, state.Identity)
		return fmt.Errorf("hydrate OpenCode attention state: %w", err)
	}
	if populated {
		state := options.Reducer.ApplySnapshot(snapshot)
		if state.Phase == attention.PhaseUnknown || options.Reducer.Invalid() {
			_ = options.Publisher.Publish(state.Phase, state.Reason, state.Identity)
			return errors.New("hydrate OpenCode attention state: invalid snapshot model")
		}
		if err := options.Publisher.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			return fmt.Errorf("publish OpenCode snapshot attention: %w", err)
		}
	}
	if options.OnReady != nil {
		options.OnReady()
	}

	reader := bufio.NewReaderSize(response.Body, 32*1024)
	var data []byte
	for {
		line, readErr := readSSELine(reader)
		if readErr != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("observe OpenCode events: %w", ctx.Err())
			}
			if errors.Is(readErr, io.EOF) {
				return errors.New("OpenCode event stream ended")
			}
			return fmt.Errorf("read OpenCode event stream: %w", readErr)
		}
		if len(line) == 0 {
			if len(data) == 0 {
				continue
			}
			event, err := DecodeEvent(data)
			data = data[:0]
			if err != nil {
				state := options.Reducer.Invalidate()
				_ = options.Publisher.Publish(state.Phase, state.Reason, state.Identity)
				return fmt.Errorf("decode OpenCode SSE event: %w", err)
			}
			state := options.Reducer.Apply(event)
			if err := options.Publisher.Publish(state.Phase, state.Reason, state.Identity); err != nil {
				return fmt.Errorf("publish OpenCode attention: %w", err)
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := strings.Cut(string(line), ":")
		if !found || field != "data" {
			continue
		}
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		additional := len(value)
		if len(data) > 0 {
			additional++
		}
		if len(data)+additional > MaxSSEEventBytes {
			return fmt.Errorf("OpenCode SSE event exceeds %d bytes", MaxSSEEventBytes)
		}
		if len(data) > 0 {
			data = append(data, '\n')
		}
		data = append(data, value...)
	}
}

func readSSELine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxSSELineBytes {
			return nil, fmt.Errorf("OpenCode SSE line exceeds %d bytes", maxSSELineBytes)
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return nil, err
		}
		line = line[:len(line)-1]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		return line, nil
	}
}
