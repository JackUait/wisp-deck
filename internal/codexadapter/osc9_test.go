package codexadapter

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestOSC9ParserRecognizesPlainAndTmuxFramesAcrossEverySplit(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name  string
		frame []byte
		want  string
	}{
		{
			name:  "plain BEL",
			frame: []byte("\x1b]9;agent-turn-complete\x07"),
			want:  "agent-turn-complete",
		},
		{
			name:  "plain ST",
			frame: []byte("\x1b]9;turn complete\x1b\\"),
			want:  "turn complete",
		},
		{
			name:  "tmux wrapped BEL",
			frame: []byte("\x1bPtmux;\x1b\x1b]9;agent-turn-complete\x07\x1b\\"),
			want:  "agent-turn-complete",
		},
		{
			name:  "tmux wrapped ST with every embedded ESC doubled",
			frame: []byte("\x1bPtmux;\x1b\x1b]9;turn complete\x1b\x1b\\\x1b\\"),
			want:  "turn complete",
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			want := []OSC9Event{{Message: fixture.want}}

			for split := 0; split <= len(fixture.frame); split++ {
				var parser OSC9Parser
				got := append(parser.Feed(fixture.frame[:split]), parser.Feed(fixture.frame[split:])...)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("split %d: events = %#v, want %#v", split, got, want)
				}
			}

			var parser OSC9Parser
			var got []OSC9Event
			for _, b := range fixture.frame {
				got = append(got, parser.Feed([]byte{b})...)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("one-byte feeds: events = %#v, want %#v", got, want)
			}
		})
	}
}

func TestOSC9ParserRejectsNoiseWrongOSCAndNonTmuxDCS(t *testing.T) {
	t.Parallel()

	stream := []byte(strings.Join([]string{
		"ordinary output\x1b[31mred\x1b[0m",
		"\x1b]0;window title\x07",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		"\x1bPnot-tmux;\x1b\x1b]9;must-not-escape\x07\x1b\\",
		"\x1b]9;first\x07",
		"middle",
		"\x1bPtmux;\x1b\x1b]9;second\x07\x1b\\",
	}, ""))

	var parser OSC9Parser
	got := parser.Feed(stream)
	want := []OSC9Event{{Message: "first"}, {Message: "second"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestOSC9ParserDoesNotModifyOrRetainInputBytes(t *testing.T) {
	t.Parallel()

	input := []byte("prefix\x1b]9;copied-message\x07suffix")
	original := append([]byte(nil), input...)
	var parser OSC9Parser
	got := parser.Feed(input)
	if !bytes.Equal(input, original) {
		t.Fatalf("Feed modified input: got %q, want %q", input, original)
	}
	if len(got) != 1 || got[0].Message != "copied-message" {
		t.Fatalf("events = %#v", got)
	}

	for i := range input {
		input[i] = 'x'
	}
	if got[0].Message != "copied-message" {
		t.Fatalf("event aliases caller input: %q", got[0].Message)
	}
}

func TestOSC9ParserRetainsAtMostExactly64KiB(t *testing.T) {
	t.Parallel()

	const capBytes = 64 * 1024
	if MaxOSC9RetainedBytes != capBytes {
		t.Fatalf("MaxOSC9RetainedBytes = %d, want %d", MaxOSC9RetainedBytes, capBytes)
	}

	var parser OSC9Parser
	prefix := []byte("\x1b]9;")
	if got := parser.Feed(append(prefix, bytes.Repeat([]byte{'a'}, capBytes)...)); len(got) != 0 {
		t.Fatalf("unterminated frame emitted %#v", got)
	}
	if got := parser.RetainedBytes(); got != capBytes {
		t.Fatalf("retained bytes at limit = %d, want %d", got, capBytes)
	}
	got := parser.Feed([]byte{'\x07'})
	if len(got) != 1 || len(got[0].Message) != capBytes {
		t.Fatalf("limit-sized frame events = %#v", got)
	}
	if got := parser.RetainedBytes(); got != 0 {
		t.Fatalf("retained bytes after completion = %d, want 0", got)
	}
}

func TestOSC9ParserDiscardsOversizeFrameAndRecoversInSameChunk(t *testing.T) {
	t.Parallel()

	stream := make([]byte, 0, MaxOSC9RetainedBytes+64)
	stream = append(stream, []byte("\x1b]9;")...)
	stream = append(stream, bytes.Repeat([]byte{'z'}, MaxOSC9RetainedBytes+1)...)
	stream = append(stream, '\x07')
	stream = append(stream, []byte("noise\x1b]9;recovered\x1b\\tail")...)

	var parser OSC9Parser
	got := parser.Feed(stream)
	want := []OSC9Event{{Message: "recovered"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	if retained := parser.RetainedBytes(); retained != 0 {
		t.Fatalf("retained bytes = %d, want 0", retained)
	}
}

func TestOSC9ParserOversizeEscapeStillFindsFollowingTerminator(t *testing.T) {
	t.Parallel()

	stream := make([]byte, 0, MaxOSC9RetainedBytes+64)
	stream = append(stream, []byte("\x1b]9;")...)
	stream = append(stream, bytes.Repeat([]byte{'z'}, MaxOSC9RetainedBytes)...)
	// The first ESC is the byte that exceeds the payload limit. The second ESC
	// begins ST and must still terminate discard mode.
	stream = append(stream, []byte("\x1b\x1b\\")...)
	stream = append(stream, []byte("\x1b]9;recovered-after-escape\x07")...)

	var parser OSC9Parser
	got := parser.Feed(stream)
	want := []OSC9Event{{Message: "recovered-after-escape"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestOSC9ParserRejectsTmuxWrapperTrailingDataAcrossEverySplit(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name  string
		frame []byte
	}{
		{
			name:  "ordinary junk after inner OSC",
			frame: []byte("\x1bPtmux;\x1b\x1b]9;first\x07junk\x1b\\"),
		},
		{
			name:  "second inner OSC",
			frame: []byte("\x1bPtmux;\x1b\x1b]9;first\x07\x1b\x1b]9;second\x07\x1b\\"),
		},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			for split := 0; split <= len(fixture.frame); split++ {
				var parser OSC9Parser
				got := append(parser.Feed(fixture.frame[:split]), parser.Feed(fixture.frame[split:])...)
				if len(got) != 0 {
					t.Fatalf("split %d: malformed tmux wrapper emitted %#v", split, got)
				}
			}
		})
	}
}

func TestOSC9ParserRejectsTmuxWrapperLeadingOrEarlierControlAcrossEverySplit(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name  string
		frame []byte
	}{
		{
			name:  "ordinary junk before inner OSC",
			frame: []byte("\x1bPtmux;junk\x1b\x1b]9;later\x07\x1b\\"),
		},
		{
			name:  "wrong inner OSC before OSC9",
			frame: []byte("\x1bPtmux;\x1b\x1b]0;title\x07\x1b\x1b]9;later\x07\x1b\\"),
		},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			for split := 0; split <= len(fixture.frame); split++ {
				var parser OSC9Parser
				got := append(parser.Feed(fixture.frame[:split]), parser.Feed(fixture.frame[split:])...)
				if len(got) != 0 {
					t.Fatalf("split %d: malformed tmux wrapper emitted %#v", split, got)
				}
			}
		})
	}

	t.Run("oversize inner OSC9 before OSC9", func(t *testing.T) {
		t.Parallel()
		prefix := []byte("\x1bPtmux;\x1b\x1b]9;")
		frame := make([]byte, 0, MaxOSC9RetainedBytes+64)
		frame = append(frame, prefix...)
		frame = append(frame, bytes.Repeat([]byte{'z'}, MaxOSC9RetainedBytes+1)...)
		frame = append(frame, []byte("\x07\x1b\x1b]9;later\x07\x1b\\")...)

		payloadStart := len(prefix)
		secondOSCStart := payloadStart + MaxOSC9RetainedBytes + 2
		splits := []int{
			0,
			len(prefix) - 1,
			payloadStart,
			payloadStart + MaxOSC9RetainedBytes - 1,
			payloadStart + MaxOSC9RetainedBytes,
			payloadStart + MaxOSC9RetainedBytes + 1,
			secondOSCStart,
			secondOSCStart + 1,
			len(frame) - 1,
			len(frame),
		}
		for _, split := range splits {
			var parser OSC9Parser
			got := append(parser.Feed(frame[:split]), parser.Feed(frame[split:])...)
			if len(got) != 0 {
				t.Fatalf("split %d: malformed tmux wrapper emitted %#v", split, got)
			}
		}

		var parser OSC9Parser
		for boundary := range frame {
			if got := parser.Feed(frame[boundary : boundary+1]); len(got) != 0 {
				t.Fatalf("byte boundary %d: malformed tmux wrapper emitted %#v", boundary, got)
			}
		}
	})
}

func TestOSC9ParserRejectsDiscardedTmuxInnerSTBeforeLaterOSC9(t *testing.T) {
	t.Parallel()

	t.Run("wrong inner OSC terminated by ST", func(t *testing.T) {
		t.Parallel()
		frame := []byte("\x1bPtmux;\x1b\x1b]0;title\x1b\x1b\\\x1b\x1b]9;later\x07\x1b\\")

		var whole OSC9Parser
		if got := whole.Feed(frame); len(got) != 0 {
			t.Fatalf("whole frame emitted %#v", got)
		}
		for split := 0; split <= len(frame); split++ {
			var parser OSC9Parser
			got := append(parser.Feed(frame[:split]), parser.Feed(frame[split:])...)
			if len(got) != 0 {
				t.Fatalf("split %d emitted %#v", split, got)
			}
		}
		var bytewise OSC9Parser
		for boundary := range frame {
			if got := bytewise.Feed(frame[boundary : boundary+1]); len(got) != 0 {
				t.Fatalf("byte boundary %d emitted %#v", boundary, got)
			}
		}
	})

	t.Run("oversize inner OSC9 terminated by ST", func(t *testing.T) {
		t.Parallel()
		prefix := []byte("\x1bPtmux;\x1b\x1b]9;")
		frame := make([]byte, 0, MaxOSC9RetainedBytes+64)
		frame = append(frame, prefix...)
		frame = append(frame, bytes.Repeat([]byte{'z'}, MaxOSC9RetainedBytes+1)...)
		frame = append(frame, []byte("\x1b\x1b\\\x1b\x1b]9;later\x07\x1b\\")...)

		var whole OSC9Parser
		if got := whole.Feed(frame); len(got) != 0 {
			t.Fatalf("whole frame emitted %#v", got)
		}

		payloadStart := len(prefix)
		innerSTStart := payloadStart + MaxOSC9RetainedBytes + 1
		secondOSCStart := innerSTStart + 3
		splits := []int{
			0,
			len(prefix) - 1,
			payloadStart,
			payloadStart + MaxOSC9RetainedBytes - 1,
			payloadStart + MaxOSC9RetainedBytes,
			payloadStart + MaxOSC9RetainedBytes + 1,
			innerSTStart + 1,
			innerSTStart + 2,
			secondOSCStart,
			secondOSCStart + 1,
			len(frame) - 1,
			len(frame),
		}
		for _, split := range splits {
			var parser OSC9Parser
			got := append(parser.Feed(frame[:split]), parser.Feed(frame[split:])...)
			if len(got) != 0 {
				t.Fatalf("split %d emitted %#v", split, got)
			}
		}

		var bytewise OSC9Parser
		for boundary := range frame {
			if got := bytewise.Feed(frame[boundary : boundary+1]); len(got) != 0 {
				t.Fatalf("byte boundary %d emitted %#v", boundary, got)
			}
		}
	})
}

func TestOSC9FilterConsumesPlainAndTmuxFramesAcrossEverySplit(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name  string
		frame string
		want  string
	}{
		{name: "plain BEL", frame: "\x1b]9;agent-turn-complete\x07", want: "agent-turn-complete"},
		{name: "plain ST", frame: "\x1b]9;turn complete\x1b\\", want: "turn complete"},
		{name: "tmux wrapped BEL", frame: "\x1bPtmux;\x1b\x1b]9;agent-turn-complete\x07\x1b\\", want: "agent-turn-complete"},
		{name: "tmux wrapped ST", frame: "\x1bPtmux;\x1b\x1b]9;turn complete\x1b\x1b\\\x1b\\", want: "turn complete"},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			input := []byte("before" + fixture.frame + "after")
			wantEvents := []OSC9Event{{Message: fixture.want}}

			for split := 0; split <= len(input); split++ {
				var filter OSC9Filter
				firstOutput, firstEvents := filter.Feed(input[:split])
				secondOutput, secondEvents := filter.Feed(input[split:])
				gotOutput := append(append(firstOutput, secondOutput...), filter.Flush()...)
				gotEvents := append(firstEvents, secondEvents...)
				if string(gotOutput) != "beforeafter" {
					t.Fatalf("split %d: output = %q, want %q", split, gotOutput, "beforeafter")
				}
				if !reflect.DeepEqual(gotEvents, wantEvents) {
					t.Fatalf("split %d: events = %#v, want %#v", split, gotEvents, wantEvents)
				}
			}

			var filter OSC9Filter
			var gotOutput []byte
			var gotEvents []OSC9Event
			for _, b := range input {
				output, events := filter.Feed([]byte{b})
				gotOutput = append(gotOutput, output...)
				gotEvents = append(gotEvents, events...)
			}
			gotOutput = append(gotOutput, filter.Flush()...)
			if string(gotOutput) != "beforeafter" {
				t.Fatalf("one-byte feeds: output = %q, want %q", gotOutput, "beforeafter")
			}
			if !reflect.DeepEqual(gotEvents, wantEvents) {
				t.Fatalf("one-byte feeds: events = %#v, want %#v", gotEvents, wantEvents)
			}
		})
	}
}

func TestOSC9FilterPreservesEveryNonNotificationByte(t *testing.T) {
	t.Parallel()

	input := []byte(strings.Join([]string{
		"ordinary",
		"\x1b[31mred\x1b[0m",
		"\x1b]0;window title\x07",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		"\x1bPnot-tmux;opaque\x1b\\",
		"\x1bPtmux;\x1b\x1b]0;inner title\x07\x1b\\",
		"\x1b]0;title contains \x1b]9;opaque text\x07",
		"\x1bPnot-tmux;opaque \x1b]9;not a terminal notification\x07 payload\x1b\\",
		"\x1bPtmux;junk\x1b\x1b]9;invalid passthrough\x07\x1b\\",
	}, "|"))

	for split := 0; split <= len(input); split++ {
		var filter OSC9Filter
		first, firstEvents := filter.Feed(input[:split])
		second, secondEvents := filter.Feed(input[split:])
		got := append(append(first, second...), filter.Flush()...)
		if !bytes.Equal(got, input) {
			t.Fatalf("split %d: output = %q, want byte-identical %q", split, got, input)
		}
		if len(firstEvents)+len(secondEvents) != 0 {
			t.Fatalf("split %d: non-notification emitted events %#v %#v", split, firstEvents, secondEvents)
		}
	}
}

func TestOSC9FilterFlushesOnlyUnconfirmedPrefixesAtEOF(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"\x1b", "\x1b]", "\x1b]9", "\x1bP", "\x1bPtmux;\x1b\x1b]9"} {
		var filter OSC9Filter
		output, events := filter.Feed([]byte(input))
		output = append(output, filter.Flush()...)
		if string(output) != input || len(events) != 0 {
			t.Fatalf("unconfirmed %q = output %q, events %#v", input, output, events)
		}
	}

	for _, input := range []string{"\x1b]9;partial", "\x1bPtmux;\x1b\x1b]9;partial"} {
		var filter OSC9Filter
		output, events := filter.Feed([]byte(input))
		output = append(output, filter.Flush()...)
		if len(output) != 0 || len(events) != 0 {
			t.Fatalf("confirmed incomplete %q = output %q, events %#v", input, output, events)
		}
	}
}

func TestOSC9FilterRecoversAfterRejectedTmuxWrapperEndsAtPrefixMismatch(t *testing.T) {
	t.Parallel()

	prefix := "\x1bPtmux;\x1b\\"
	input := []byte(prefix + "before\x1b]9;done\x07after")
	for split := 0; split <= len(input); split++ {
		var filter OSC9Filter
		first, firstEvents := filter.Feed(input[:split])
		second, secondEvents := filter.Feed(input[split:])
		output := append(append(first, second...), filter.Flush()...)
		events := append(firstEvents, secondEvents...)
		if string(output) != prefix+"beforeafter" {
			t.Fatalf("split %d: output = %q, want %q", split, output, prefix+"beforeafter")
		}
		if !reflect.DeepEqual(events, []OSC9Event{{Message: "done"}}) {
			t.Fatalf("split %d: events = %#v", split, events)
		}
	}
}

func TestOSC9FilterEnforcesPayloadBoundAndRecovers(t *testing.T) {
	t.Parallel()

	limitPayload := strings.Repeat("a", MaxOSC9RetainedBytes)
	var atLimit OSC9Filter
	output, events := atLimit.Feed([]byte("left\x1b]9;" + limitPayload + "\x07right"))
	output = append(output, atLimit.Flush()...)
	if string(output) != "leftright" {
		t.Fatalf("limit output = %q", output)
	}
	if len(events) != 1 || events[0].Message != limitPayload {
		t.Fatalf("limit events = count %d, message bytes %d", len(events), func() int {
			if len(events) == 0 {
				return 0
			}
			return len(events[0].Message)
		}())
	}
	if retained := atLimit.RetainedBytes(); retained != 0 {
		t.Fatalf("limit retained bytes = %d, want 0", retained)
	}

	oversize := "left\x1b]9;" + strings.Repeat("z", MaxOSC9RetainedBytes+1) +
		"\x07middle\x1b]9;recovered\x07right"
	var filter OSC9Filter
	output, events = filter.Feed([]byte(oversize))
	output = append(output, filter.Flush()...)
	if string(output) != "leftmiddleright" {
		t.Fatalf("oversize output = %q, want %q", output, "leftmiddleright")
	}
	if !reflect.DeepEqual(events, []OSC9Event{{Message: "recovered"}}) {
		t.Fatalf("oversize events = %#v", events)
	}
	if retained := filter.RetainedBytes(); retained != 0 {
		t.Fatalf("oversize retained bytes = %d, want 0", retained)
	}
}
