package terminalcontrol

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestFilterSuppressesBellAndOSC9AcrossEverySplit(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name   string
		frame  string
		events []Event
	}{
		{name: "standalone BEL", frame: "\x07"},
		{name: "plain OSC9 BEL", frame: "\x1b]9;done\x07", events: []Event{{Message: "done"}}},
		{name: "plain OSC9 ST", frame: "\x1b]9;done\x1b\\", events: []Event{{Message: "done"}}},
		{name: "tmux OSC9 BEL", frame: "\x1bPtmux;\x1b\x1b]9;done\x07\x1b\\", events: []Event{{Message: "done"}}},
		{name: "tmux standalone BEL", frame: "\x1bPtmux;\x07\x1b\\"},
		{name: "tmux BEL after text", frame: "\x1bPtmux;inner\x07text\x1b\\"},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			input := []byte("before" + fixture.frame + "after")
			wantOutput := "beforeafter"
			if fixture.name == "tmux BEL after text" {
				wantOutput = "before\x1bPtmux;innertext\x1b\\after"
			}
			for split := 0; split <= len(input); split++ {
				var filter Filter
				first, firstEvents := filter.Feed(input[:split])
				second, secondEvents := filter.Feed(input[split:])
				got := append(append(first, second...), filter.Flush()...)
				if string(got) != wantOutput {
					t.Fatalf("split %d: output = %q, want %q", split, got, wantOutput)
				}
				if events := append(firstEvents, secondEvents...); !reflect.DeepEqual(events, fixture.events) {
					t.Fatalf("split %d: events = %#v, want %#v", split, events, fixture.events)
				}
			}
		})
	}
}

func TestFilterPreservesUnrelatedControlsByteForByte(t *testing.T) {
	t.Parallel()

	input := []byte(strings.Join([]string{
		"plain UTF-8 λ",
		"\x1b[31mred\x1b[0m",
		"\x1b]0;title\x07",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		"\x1bPopaque\x07data\x1b\\",
		"\x1bPtmux;\x1b\x1b]0;inner title\x07\x1b\\",
	}, "|"))

	for split := 0; split <= len(input); split++ {
		var filter Filter
		first, firstEvents := filter.Feed(input[:split])
		second, secondEvents := filter.Feed(input[split:])
		got := append(append(first, second...), filter.Flush()...)
		if !bytes.Equal(got, input) {
			t.Fatalf("split %d: output = %q, want byte-identical %q", split, got, input)
		}
		if len(firstEvents)+len(secondEvents) != 0 {
			t.Fatalf("split %d: unrelated controls emitted events", split)
		}
	}
}

func TestFilterIsBoundedAndRecoversAfterOversizeOSC9(t *testing.T) {
	t.Parallel()

	stream := "left\x1b]9;" + strings.Repeat("z", MaxRetainedBytes+1) +
		"\x07middle\x07right\x1b]9;recovered\x07tail"
	var filter Filter
	output, events := filter.Feed([]byte(stream))
	output = append(output, filter.Flush()...)
	if string(output) != "leftmiddlerighttail" {
		t.Fatalf("output = %q", output)
	}
	if !reflect.DeepEqual(events, []Event{{Message: "recovered"}}) {
		t.Fatalf("events = %#v", events)
	}
	if retained := filter.RetainedBytes(); retained != 0 {
		t.Fatalf("retained = %d, want 0", retained)
	}
}

func TestFilterEOFPolicy(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"\x1b", "\x1b]", "\x1b]9", "\x1bPtmux;"} {
		var filter Filter
		output, _ := filter.Feed([]byte(input))
		output = append(output, filter.Flush()...)
		if string(output) != input {
			t.Fatalf("ambiguous prefix %q flushed as %q", input, output)
		}
	}
	for _, input := range []string{"\x07", "\x1b]9;partial", "\x1bPtmux;\x1b\x1b]9;partial"} {
		var filter Filter
		output, _ := filter.Feed([]byte(input))
		output = append(output, filter.Flush()...)
		if len(output) != 0 {
			t.Fatalf("confirmed notification %q flushed as %q", input, output)
		}
	}
}
