package cireport

import (
	"strings"
	"testing"
)

// A go test -json stream, as CI captures it (with 2>&1, so stray non-JSON lines
// from the toolchain can be interleaved).
func jsonLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestReport_names_the_failing_test_and_shows_its_output(t *testing.T) {
	in := jsonLines(
		`{"Action":"run","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestDraftSurvives"}`,
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestDraftSurvives","Output":"    draft_test.go:42: got \"\", want \"hello\"\n"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestDraftSurvives","Elapsed":0.1}`,
		`{"Action":"pass","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestOther"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash","Elapsed":0.2}`,
	)

	r := Parse(strings.NewReader(in))

	if r.Failed != 1 || r.Passed != 1 {
		t.Fatalf("got %d failed / %d passed, want 1/1", r.Failed, r.Passed)
	}
	text := r.Text()
	if !strings.Contains(text, "TestDraftSurvives") {
		t.Errorf("report does not name the failing test:\n%s", text)
	}
	if !strings.Contains(text, "test/bash") {
		t.Errorf("report does not name the package:\n%s", text)
	}
	if !strings.Contains(text, `got "", want "hello"`) {
		t.Errorf("report does not show the assertion output:\n%s", text)
	}
	if !r.HasFailures() {
		t.Error("HasFailures() = false, want true")
	}
}

func TestReport_reports_leaf_subtests_not_their_parent(t *testing.T) {
	in := jsonLines(
		`{"Action":"output","Package":"p","Test":"TestParent/sub_case","Output":"    x_test.go:9: boom\n"}`,
		`{"Action":"fail","Package":"p","Test":"TestParent/sub_case"}`,
		`{"Action":"fail","Package":"p","Test":"TestParent"}`,
		`{"Action":"fail","Package":"p"}`,
	)

	r := Parse(strings.NewReader(in))

	if len(r.Failures) != 1 {
		t.Fatalf("got %d failures, want 1 (the leaf subtest only): %+v", len(r.Failures), r.Failures)
	}
	if r.Failures[0].Test != "TestParent/sub_case" {
		t.Errorf("got failure for %q, want the leaf subtest", r.Failures[0].Test)
	}
}

func TestReport_surfaces_a_build_failure_that_has_no_test(t *testing.T) {
	in := jsonLines(
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/internal/tui","Output":"FAIL\tgithub.com/jackuait/wisp-deck/internal/tui [build failed]\n"}`,
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/internal/tui","Output":"./menu.go:12:2: undefined: Foo\n"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/internal/tui","Elapsed":0}`,
	)

	r := Parse(strings.NewReader(in))

	if !r.HasFailures() {
		t.Fatal("a package that failed with no test must still be reported as a failure")
	}
	text := r.Text()
	if !strings.Contains(text, "undefined: Foo") {
		t.Errorf("build error not shown:\n%s", text)
	}
}

func TestReport_keeps_non_json_toolchain_noise(t *testing.T) {
	in := jsonLines(
		`# github.com/jackuait/wisp-deck/internal/tui`,
		`./menu.go:12:2: undefined: Foo`,
	)

	r := Parse(strings.NewReader(in))

	if !r.HasFailures() {
		t.Fatal("toolchain errors printed outside the JSON stream must fail the report")
	}
	if !strings.Contains(r.Text(), "undefined: Foo") {
		t.Errorf("toolchain error not shown:\n%s", r.Text())
	}
}

// `go test` writes module-fetch progress to stderr, which CI merges into the
// JSON stream. It is chatter, not an error — treating it as one would fail a
// green run.
func TestReport_ignores_module_download_chatter(t *testing.T) {
	in := jsonLines(
		`go: downloading github.com/creack/pty v1.1.24`,
		`go: downloading github.com/charmbracelet/bubbletea v1.3.4`,
		`{"Action":"pass","Package":"p","Test":"TestA"}`,
		`{"Action":"pass","Package":"p"}`,
	)

	r := Parse(strings.NewReader(in))

	if r.HasFailures() {
		t.Fatalf("module-download chatter must not fail the run:\n%s", r.Text())
	}
	if len(r.Annotations()) != 0 {
		t.Errorf("no annotations should be emitted for download chatter: %v", r.Annotations())
	}
}

func TestReport_still_fails_on_a_real_toolchain_error_beside_the_chatter(t *testing.T) {
	in := jsonLines(
		`go: downloading github.com/creack/pty v1.1.24`,
		`go: errors parsing go.mod:`,
		`./menu.go:12:2: undefined: Foo`,
	)

	r := Parse(strings.NewReader(in))

	if !r.HasFailures() {
		t.Fatal("a real toolchain error must still fail, even when download chatter is present")
	}
	if strings.Contains(r.Text(), "downloading") {
		t.Errorf("chatter should be filtered out of the report:\n%s", r.Text())
	}
	if !strings.Contains(r.Text(), "undefined: Foo") {
		t.Errorf("the real error must survive:\n%s", r.Text())
	}
}

func TestReport_says_everything_passed_when_nothing_failed(t *testing.T) {
	in := jsonLines(
		`{"Action":"pass","Package":"p","Test":"TestA"}`,
		`{"Action":"pass","Package":"p","Test":"TestB"}`,
		`{"Action":"pass","Package":"p"}`,
	)

	r := Parse(strings.NewReader(in))

	if r.HasFailures() {
		t.Fatal("HasFailures() = true with no failures")
	}
	if !strings.Contains(r.Text(), "2") || !strings.Contains(strings.ToLower(r.Text()), "passed") {
		t.Errorf("passing report should state the count:\n%s", r.Text())
	}
}

func TestReport_treats_empty_output_as_a_failure(t *testing.T) {
	r := Parse(strings.NewReader(""))

	if !r.HasFailures() {
		t.Fatal("an empty test stream means the run died before testing; must not be reported as success")
	}
	if !strings.Contains(strings.ToLower(r.Text()), "no test output") {
		t.Errorf("empty report should explain itself:\n%s", r.Text())
	}
}

func TestReport_emits_one_github_annotation_per_failure(t *testing.T) {
	in := jsonLines(
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestA","Output":"    a_test.go:7: got 1, want 2\n"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestA"}`,
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestB","Output":"    b_test.go:3: nope\n"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestB"}`,
	)

	r := Parse(strings.NewReader(in))
	ann := r.Annotations()

	if len(ann) != 2 {
		t.Fatalf("got %d annotations, want 2:\n%s", len(ann), strings.Join(ann, "\n"))
	}
	if !strings.HasPrefix(ann[0], "::error ") {
		t.Errorf("annotation is not a GitHub error command: %q", ann[0])
	}
	if !strings.Contains(ann[0], "TestA") {
		t.Errorf("annotation does not name the test: %q", ann[0])
	}
	for _, a := range ann {
		if strings.Contains(a, "\n") {
			t.Errorf("annotation must encode newlines as %%0A, got a raw newline: %q", a)
		}
	}
}

func TestReport_annotation_points_at_the_source_line(t *testing.T) {
	in := jsonLines(
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestA","Output":"    account_switch_test.go:118: expected draft to survive\n"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestA"}`,
	)

	ann := Parse(strings.NewReader(in)).Annotations()

	if len(ann) != 1 {
		t.Fatalf("got %d annotations, want 1", len(ann))
	}
	if !strings.Contains(ann[0], "file=test/bash/account_switch_test.go") {
		t.Errorf("annotation should point at the test file so GitHub renders it inline: %q", ann[0])
	}
	if !strings.Contains(ann[0], "line=118") {
		t.Errorf("annotation should carry the failing line: %q", ann[0])
	}
}

func TestReport_markdown_summary_lists_failures_with_their_output(t *testing.T) {
	in := jsonLines(
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestA","Output":"    a_test.go:7: got 1, want 2\n"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestA"}`,
		`{"Action":"pass","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestB"}`,
	)

	md := Parse(strings.NewReader(in)).Markdown("Bash tests")

	if !strings.Contains(md, "Bash tests") {
		t.Errorf("summary should carry the job title:\n%s", md)
	}
	if !strings.Contains(md, "TestA") {
		t.Errorf("summary should name the failing test:\n%s", md)
	}
	if !strings.Contains(md, "got 1, want 2") {
		t.Errorf("summary should include the failure output, not just the test name:\n%s", md)
	}
}

func TestReport_counts_read_as_english(t *testing.T) {
	in := jsonLines(
		`{"Action":"output","Package":"p","Test":"TestA","Output":"    a_test.go:7: nope\n"}`,
		`{"Action":"fail","Package":"p","Test":"TestA"}`,
	)

	text := Parse(strings.NewReader(in)).Text()

	if strings.Contains(text, "1 tests") {
		t.Errorf("counts should be pluralized:\n%s", text)
	}
	if !strings.Contains(text, "1 test)") {
		t.Errorf("want a singular test count:\n%s", text)
	}
}

// The pty tests dump whole terminal frames — thousands of escape-sequence
// characters on ONE line. Replayed verbatim it buries the assertion that
// actually explains the failure.
func TestReport_clips_an_enormous_single_line(t *testing.T) {
	blob := strings.Repeat(`\x1b[90m`, 900)
	in := jsonLines(
		`{"Action":"output","Package":"p","Test":"TestPty","Output":"    pty_test.go:12: expected 6 redraws, got 5. raw: `+blob+`\n"}`,
		`{"Action":"fail","Package":"p","Test":"TestPty"}`,
	)

	text := Parse(strings.NewReader(in)).Text()

	for _, line := range strings.Split(text, "\n") {
		if len(line) > maxLineChars+40 {
			t.Fatalf("a %d-char line survived; long lines must be clipped", len(line))
		}
	}
	if !strings.Contains(text, "expected 6 redraws, got 5") {
		t.Errorf("clipping must keep the head of the line, where the assertion is:\n%s", text)
	}
}

func TestReport_annotation_stays_short_enough_to_read(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines,
			`{"Action":"output","Package":"p","Test":"TestPty","Output":"    `+strings.Repeat("noise ", 60)+`\n"}`)
	}
	lines = append(lines, `{"Action":"fail","Package":"p","Test":"TestPty"}`)

	ann := Parse(strings.NewReader(jsonLines(lines...))).Annotations()

	if len(ann) != 1 {
		t.Fatalf("got %d annotations, want 1", len(ann))
	}
	if len(ann[0]) > maxAnnotationChars+200 {
		t.Errorf("annotation is %d chars; GitHub renders it in a small box, keep it skimmable", len(ann[0]))
	}
}

func TestReport_truncates_a_runaway_failure_and_says_so(t *testing.T) {
	var lines []string
	lines = append(lines, `{"Action":"run","Package":"p","Test":"TestNoisy"}`)
	for i := 0; i < 500; i++ {
		lines = append(lines, `{"Action":"output","Package":"p","Test":"TestNoisy","Output":"    spam\n"}`)
	}
	lines = append(lines, `{"Action":"fail","Package":"p","Test":"TestNoisy"}`)

	text := Parse(strings.NewReader(jsonLines(lines...))).Text()

	if strings.Count(text, "spam") > maxOutputLines {
		t.Errorf("output was not truncated: %d spam lines", strings.Count(text, "spam"))
	}
	if !strings.Contains(text, "truncated") {
		t.Errorf("truncation must be announced so nobody thinks that was all of it:\n%s", text)
	}
}
