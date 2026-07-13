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
