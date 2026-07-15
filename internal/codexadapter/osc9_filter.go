package codexadapter

// OSC9Filter consumes Codex completion notifications before PTY output reaches
// the outer terminal. The zero value is ready for use.
type OSC9Filter struct {
	parser     OSC9Parser
	mode       osc9FilterMode
	headerByte int
	candidate  []byte
	confirmed  bool
}

type osc9FilterMode uint8

const (
	filterGround osc9FilterMode = iota
	filterEscape
	filterOSCCommand
	filterOSCSeparator
	filterPlainPayload
	filterPlainPayloadEscape
	filterDCSHeader
	filterTmuxFirstEscape
	filterTmuxSecondEscape
	filterTmuxBracket
	filterTmuxCommand
	filterTmuxSeparator
	filterTmuxPayload
	filterTmuxPayloadEscape
	filterPassOSC
	filterPassOSCEscape
	filterPassDCS
	filterPassDCSEscape
	filterPassTmux
	filterPassTmuxEscape
)

// Feed returns ordinary terminal bytes and complete OSC 9 events in wire
// order. Confirmed OSC 9 frames are omitted from output, including across
// arbitrary input boundaries.
func (f *OSC9Filter) Feed(data []byte) ([]byte, []OSC9Event) {
	events := f.parser.Feed(data)
	output := make([]byte, 0, len(data))
	for _, b := range data {
		f.filterByte(b, &output)
	}
	return output, events
}

// Flush completes the stream. An ambiguous non-notification prefix is emitted
// byte-for-byte; an incomplete confirmed notification remains private.
func (f *OSC9Filter) Flush() []byte {
	var output []byte
	if !f.confirmed {
		output = append(output, f.candidate...)
	}
	*f = OSC9Filter{}
	return output
}

// RetainedBytes reports all variable-sized state held across Feed calls.
func (f *OSC9Filter) RetainedBytes() int {
	return f.parser.RetainedBytes() + len(f.candidate)
}

func (f *OSC9Filter) filterByte(b byte, output *[]byte) {
	switch f.mode {
	case filterGround:
		if b == escape {
			f.candidate = append(f.candidate[:0], b)
			f.mode = filterEscape
			return
		}
		*output = append(*output, b)

	case filterEscape:
		switch b {
		case ']':
			f.candidate = append(f.candidate, b)
			f.mode = filterOSCCommand
		case 'P':
			f.candidate = append(f.candidate, b)
			f.headerByte = 0
			f.mode = filterDCSHeader
		default:
			f.flushCandidate(output)
			f.resetFilter()
			f.filterByte(b, output)
		}

	case filterOSCCommand:
		f.candidate = append(f.candidate, b)
		if b == '9' {
			f.mode = filterOSCSeparator
			return
		}
		f.rejectOSCCandidate(b, output)

	case filterOSCSeparator:
		f.candidate = append(f.candidate, b)
		if b == ';' {
			f.confirm()
			f.mode = filterPlainPayload
			return
		}
		f.rejectOSCCandidate(b, output)

	case filterPlainPayload:
		switch b {
		case bel:
			f.resetFilter()
		case escape:
			f.mode = filterPlainPayloadEscape
		}

	case filterPlainPayloadEscape:
		switch b {
		case '\\', bel:
			f.resetFilter()
		case escape:
			f.mode = filterPlainPayloadEscape
		default:
			f.mode = filterPlainPayload
		}

	case filterDCSHeader:
		f.candidate = append(f.candidate, b)
		if b != tmuxHeader[f.headerByte] {
			f.rejectDCSCandidate(b, false, output)
			return
		}
		f.headerByte++
		if f.headerByte == len(tmuxHeader) {
			f.mode = filterTmuxFirstEscape
		}

	case filterTmuxFirstEscape:
		f.matchTmuxPrefixByte(b, escape, filterTmuxSecondEscape, output)

	case filterTmuxSecondEscape:
		f.candidate = append(f.candidate, b)
		switch b {
		case escape:
			f.mode = filterTmuxBracket
		case '\\':
			// The first ESC was an outer ST introducer, not a doubled
			// inner ESC. The rejected wrapper ends here.
			f.flushCandidate(output)
			f.resetFilter()
		default:
			f.rejectDCSCandidate(b, true, output)
		}

	case filterTmuxBracket:
		f.matchTmuxPrefixByte(b, ']', filterTmuxCommand, output)

	case filterTmuxCommand:
		f.matchTmuxPrefixByte(b, '9', filterTmuxSeparator, output)

	case filterTmuxSeparator:
		f.candidate = append(f.candidate, b)
		if b == ';' {
			f.confirm()
			f.mode = filterTmuxPayload
			return
		}
		f.rejectDCSCandidate(b, true, output)

	case filterTmuxPayload:
		if b == escape {
			f.mode = filterTmuxPayloadEscape
		}

	case filterTmuxPayloadEscape:
		switch b {
		case '\\':
			f.resetFilter()
		case escape:
			// A doubled ESC is decoded by tmux as one inner ESC. The byte
			// after the pair is inner payload, not the outer terminator.
			f.mode = filterTmuxPayload
		default:
			f.mode = filterTmuxPayload
		}

	case filterPassOSC:
		*output = append(*output, b)
		switch b {
		case bel:
			f.resetFilter()
		case escape:
			f.mode = filterPassOSCEscape
		}

	case filterPassOSCEscape:
		*output = append(*output, b)
		switch b {
		case '\\', bel:
			f.resetFilter()
		case escape:
			f.mode = filterPassOSCEscape
		default:
			f.mode = filterPassOSC
		}

	case filterPassDCS:
		*output = append(*output, b)
		if b == escape {
			f.mode = filterPassDCSEscape
		}

	case filterPassDCSEscape:
		*output = append(*output, b)
		switch b {
		case '\\':
			f.resetFilter()
		case escape:
			f.mode = filterPassDCSEscape
		default:
			f.mode = filterPassDCS
		}

	case filterPassTmux:
		*output = append(*output, b)
		if b == escape {
			f.mode = filterPassTmuxEscape
		}

	case filterPassTmuxEscape:
		*output = append(*output, b)
		switch b {
		case '\\':
			f.resetFilter()
		case escape:
			// A doubled ESC is inner payload, so its following byte
			// cannot complete the outer wrapper.
			f.mode = filterPassTmux
		default:
			f.mode = filterPassTmux
		}
	}
}

func (f *OSC9Filter) rejectOSCCandidate(b byte, output *[]byte) {
	f.flushCandidate(output)
	f.headerByte = 0
	f.candidate = f.candidate[:0]
	f.confirmed = false
	switch b {
	case bel:
		f.mode = filterGround
	case escape:
		f.mode = filterPassOSCEscape
	default:
		f.mode = filterPassOSC
	}
}

func (f *OSC9Filter) matchTmuxPrefixByte(
	b byte,
	want byte,
	next osc9FilterMode,
	output *[]byte,
) {
	f.candidate = append(f.candidate, b)
	if b == want {
		f.mode = next
		return
	}
	f.rejectDCSCandidate(b, true, output)
}

func (f *OSC9Filter) rejectDCSCandidate(b byte, tmux bool, output *[]byte) {
	f.flushCandidate(output)
	f.headerByte = 0
	f.candidate = f.candidate[:0]
	f.confirmed = false
	if tmux {
		if b == escape {
			f.mode = filterPassTmuxEscape
		} else {
			f.mode = filterPassTmux
		}
		return
	}
	if b == escape {
		f.mode = filterPassDCSEscape
	} else {
		f.mode = filterPassDCS
	}
}

func (f *OSC9Filter) confirm() {
	f.candidate = f.candidate[:0]
	f.confirmed = true
}

func (f *OSC9Filter) flushCandidate(output *[]byte) {
	*output = append(*output, f.candidate...)
}

func (f *OSC9Filter) resetFilter() {
	f.mode = filterGround
	f.headerByte = 0
	f.candidate = f.candidate[:0]
	f.confirmed = false
}
