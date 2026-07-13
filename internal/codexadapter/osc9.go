// Package codexadapter reduces Codex's passive app-server and terminal signals
// into Wisp Deck's normalized attention protocol.
package codexadapter

const (
	// MaxOSC9RetainedBytes bounds the only variable-sized parser state. A frame
	// larger than this is discarded through its terminator so a following frame
	// in the same input chunk can still be recognized.
	MaxOSC9RetainedBytes = 64 * 1024

	escape = byte(0x1b)
	bel    = byte(0x07)
)

// OSC9Event is one complete OSC 9 notification. Message owns its bytes and is
// unaffected if the caller reuses the input passed to Feed.
type OSC9Event struct {
	Message string
}

// OSC9Parser recognizes plain OSC 9 and tmux passthrough-wrapped OSC 9 from an
// arbitrary byte stream. The zero value is ready for use.
type OSC9Parser struct {
	mode       wrapperMode
	headerByte int
	plain      plainOSC9Parser
	tmux       plainOSC9Parser
	tmuxEvent  *OSC9Event
}

type wrapperMode uint8

const (
	wrapperScan wrapperMode = iota
	wrapperEscape
	wrapperHeader
	wrapperContent
	wrapperContentEscape
	wrapperDiscard
	wrapperDiscardEscape
)

const tmuxHeader = "tmux;"

// Feed observes data without changing it and returns every complete event in
// wire order. Incomplete state remains in p for the next call.
func (p *OSC9Parser) Feed(data []byte) []OSC9Event {
	var events []OSC9Event
	for _, b := range data {
		switch p.mode {
		case wrapperScan:
			// Intercept DCS only at terminal ground. ESC P inside an OSC
			// payload is payload, not a new tmux wrapper.
			if b == escape && p.plain.atGround() {
				p.mode = wrapperEscape
				continue
			}
			if event, ok := p.plain.feedByte(b); ok {
				events = append(events, event)
			}

		case wrapperEscape:
			if b == 'P' {
				p.mode = wrapperHeader
				p.headerByte = 0
				continue
			}
			if event, ok := p.plain.feedByte(escape); ok {
				events = append(events, event)
			}
			if event, ok := p.plain.feedByte(b); ok {
				events = append(events, event)
			}
			p.mode = wrapperScan

		case wrapperHeader:
			if b == tmuxHeader[p.headerByte] {
				p.headerByte++
				if p.headerByte == len(tmuxHeader) {
					p.mode = wrapperContent
					p.tmux.reset()
					p.tmuxEvent = nil
				}
				continue
			}
			if b == escape {
				p.mode = wrapperDiscardEscape
			} else {
				p.mode = wrapperDiscard
			}

		case wrapperContent:
			if b == escape {
				p.mode = wrapperContentEscape
				continue
			}
			if p.tmuxEvent != nil {
				p.discardTmuxWrapper()
				continue
			}
			if !p.feedTmuxByte(b) {
				p.discardTmuxWrapper()
			}

		case wrapperContentEscape:
			if p.tmuxEvent != nil {
				if b == '\\' {
					events = append(events, *p.tmuxEvent)
					p.resetWrapper()
				} else {
					p.discardTmuxWrapper()
				}
				continue
			}
			switch b {
			case escape:
				// tmux doubles every ESC embedded in its DCS payload.
				if p.feedTmuxByte(escape) {
					p.mode = wrapperContent
				} else {
					p.discardTmuxWrapper()
				}
			case '\\':
				// Do not expose an inner event until the complete outer DCS
				// wrapper has been validated.
				if p.tmuxEvent != nil {
					events = append(events, *p.tmuxEvent)
				}
				p.resetWrapper()
			default:
				// A tmux passthrough cannot contain a lone ESC. Discard the
				// malformed wrapper through its outer ST.
				p.tmux.reset()
				p.tmuxEvent = nil
				p.mode = wrapperDiscard
			}

		case wrapperDiscard:
			if b == escape {
				p.mode = wrapperDiscardEscape
			}

		case wrapperDiscardEscape:
			switch b {
			case '\\':
				p.resetWrapper()
			case escape:
				// A doubled ESC is one decoded inner ESC. Its following byte
				// is inner data; only a subsequent single ESC can begin outer ST.
				p.mode = wrapperDiscard
			default:
				p.mode = wrapperDiscard
			}
		}
	}
	return events
}

// RetainedBytes reports variable-sized data kept across Feed calls. It exists
// to make the parser's hard memory bound directly testable.
func (p *OSC9Parser) RetainedBytes() int {
	retained := p.plain.retainedBytes() + p.tmux.retainedBytes()
	if p.tmuxEvent != nil {
		retained += len(p.tmuxEvent.Message)
	}
	return retained
}

func (p *OSC9Parser) feedTmuxByte(b byte) bool {
	if p.tmuxEvent != nil {
		return false
	}
	// The plain parser is intentionally a recovering stream scanner. A tmux
	// passthrough is stricter: its decoded payload must begin with exactly one
	// OSC 9 control sequence, with no ignored bytes or earlier control.
	switch p.tmux.state {
	case plainGround:
		if b != escape {
			return false
		}
	case plainEscape:
		if b != ']' {
			return false
		}
	case plainCommand:
		if b != '9' {
			return false
		}
	case plainSeparator:
		if b != ';' {
			return false
		}
	case plainDiscard, plainDiscardEscape:
		return false
	}
	if event, ok := p.tmux.feedByte(b); ok {
		p.tmuxEvent = &event
	}
	return p.tmux.state != plainDiscard && p.tmux.state != plainDiscardEscape
}

func (p *OSC9Parser) resetWrapper() {
	p.mode = wrapperScan
	p.headerByte = 0
	p.tmux.reset()
	p.tmuxEvent = nil
}

func (p *OSC9Parser) discardTmuxWrapper() {
	p.tmux.reset()
	p.tmuxEvent = nil
	p.mode = wrapperDiscard
}

type plainState uint8

const (
	plainGround plainState = iota
	plainEscape
	plainCommand
	plainSeparator
	plainPayload
	plainPayloadEscape
	plainDiscard
	plainDiscardEscape
)

type plainOSC9Parser struct {
	state   plainState
	payload []byte
}

func (p *plainOSC9Parser) atGround() bool {
	return p.state == plainGround
}

func (p *plainOSC9Parser) retainedBytes() int {
	return len(p.payload)
}

func (p *plainOSC9Parser) reset() {
	p.state = plainGround
	p.payload = nil
}

func (p *plainOSC9Parser) feedByte(b byte) (OSC9Event, bool) {
	switch p.state {
	case plainGround:
		if b == escape {
			p.state = plainEscape
		}

	case plainEscape:
		switch b {
		case ']':
			p.state = plainCommand
		case escape:
			p.state = plainEscape
		default:
			p.state = plainGround
		}

	case plainCommand:
		if b == '9' {
			p.state = plainSeparator
		} else {
			p.beginDiscard(b)
		}

	case plainSeparator:
		if b == ';' {
			p.state = plainPayload
			p.payload = p.payload[:0]
		} else {
			p.beginDiscard(b)
		}

	case plainPayload:
		switch b {
		case bel:
			return p.complete()
		case escape:
			p.state = plainPayloadEscape
		default:
			p.appendPayload(b)
		}

	case plainPayloadEscape:
		switch b {
		case '\\':
			return p.complete()
		case bel:
			if !p.appendPayload(escape) {
				return OSC9Event{}, false
			}
			return p.complete()
		case escape:
			if p.appendPayload(escape) {
				p.state = plainPayloadEscape
			}
		default:
			if p.appendPayload(escape) {
				p.appendPayload(b)
			}
		}

	case plainDiscard:
		switch b {
		case bel:
			p.reset()
		case escape:
			p.state = plainDiscardEscape
		}

	case plainDiscardEscape:
		switch b {
		case '\\':
			p.reset()
		case escape:
			p.state = plainDiscardEscape
		default:
			p.state = plainDiscard
		}
	}
	return OSC9Event{}, false
}

func (p *plainOSC9Parser) appendPayload(b byte) bool {
	if len(p.payload) == MaxOSC9RetainedBytes {
		p.payload = nil
		if b == escape {
			p.state = plainDiscardEscape
		} else {
			p.state = plainDiscard
		}
		return false
	}
	p.payload = append(p.payload, b)
	return true
}

func (p *plainOSC9Parser) beginDiscard(b byte) {
	p.payload = nil
	switch b {
	case bel:
		p.state = plainGround
	case escape:
		p.state = plainDiscardEscape
	default:
		p.state = plainDiscard
	}
}

func (p *plainOSC9Parser) complete() (OSC9Event, bool) {
	message := string(p.payload)
	p.reset()
	return OSC9Event{Message: message}, true
}
