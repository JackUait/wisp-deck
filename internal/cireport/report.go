// Package cireport turns a `go test -json` stream into something a human can
// read at a glance in a CI log.
//
// Without it, a failing job shows only "Process completed with exit code 1" and
// the reader has to scroll a raw JSON stream to find out which test broke.
package cireport

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxOutputLines caps how much of one test's output is replayed, so a single
// runaway test cannot bury the other failures.
const maxOutputLines = 60

// maxLineChars clips one line. The pty tests print whole terminal frames —
// thousands of escape-sequence characters on a single line — and the assertion
// that explains the failure is at the head of it.
const maxLineChars = 300

// maxAnnotationChars bounds the `::error` body. GitHub renders it in a small box
// on the run summary; past a few lines it stops being skimmable.
const maxAnnotationChars = 900

// maxAnnotationLines is how many lines of output an annotation carries. The
// first lines hold the assertion; the rest is context best read in the log.
const maxAnnotationLines = 5

const modulePrefix = "github.com/jackuait/wisp-deck/"

// Failure is one failed test, or one package that failed without running a test
// (a build error, a panic, a timeout).
type Failure struct {
	Package string
	Test    string // empty for a package-level failure
	Output  []string
}

// Report is the parsed result of one `go test -json` run.
type Report struct {
	Passed   int
	Failed   int
	Failures []Failure
	// Noise is output the toolchain wrote outside the JSON stream — build
	// errors, mostly. It only shows up because CI merges stderr into stdout.
	Noise []string

	empty bool
}

type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// Parse reads a `go test -json` stream (stderr may be merged in).
func Parse(r io.Reader) *Report {
	rep := &Report{}
	outputs := map[string][]string{} // package\x00test -> output lines
	failed := map[string]bool{}      // package\x00test (test may be empty)
	var order []string

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	sawLine := false

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		sawLine = true

		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Action == "" {
			if !isChatter(line) {
				rep.Noise = append(rep.Noise, line)
			}
			continue
		}

		key := ev.Package + "\x00" + ev.Test
		switch ev.Action {
		case "output":
			if out := strings.TrimRight(ev.Output, "\n"); out != "" {
				outputs[key] = append(outputs[key], out)
			}
		case "pass":
			if ev.Test != "" {
				rep.Passed++
			}
		case "fail":
			if !failed[key] {
				failed[key] = true
				order = append(order, key)
			}
			if ev.Test != "" {
				rep.Failed++
			}
		}
	}

	rep.empty = !sawLine

	for _, key := range order {
		pkg, test, _ := strings.Cut(key, "\x00")
		// A parent test fails whenever a subtest does; report the leaf, which is
		// the one carrying the actual assertion output.
		if test != "" && hasFailedChild(failed, pkg, test) {
			rep.Failed--
			continue
		}
		// A package "fail" event is redundant once its tests are listed — unless
		// no test failed, which means the package itself broke (build, panic).
		if test == "" && packageHasFailedTest(failed, pkg) {
			continue
		}
		rep.Failures = append(rep.Failures, Failure{
			Package: pkg,
			Test:    test,
			Output:  meaningfulOutput(outputs[key]),
		})
	}

	return rep
}

// chatterPrefixes are the progress lines `go` writes to stderr while resolving
// modules. CI merges stderr into the JSON stream, so they land here — but they
// say nothing went wrong, and failing a green run over them would be worse than
// the problem this package exists to fix.
var chatterPrefixes = []string{
	"go: downloading ",
	"go: finding ",
	"go: extracting ",
	"go: upgraded ",
	"go: downgraded ",
	"go: added ",
	"go: removed ",
}

func isChatter(line string) bool {
	for _, p := range chatterPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func hasFailedChild(failed map[string]bool, pkg, test string) bool {
	prefix := pkg + "\x00" + test + "/"
	for key := range failed {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func packageHasFailedTest(failed map[string]bool, pkg string) bool {
	prefix := pkg + "\x00"
	for key := range failed {
		if key != prefix && strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// meaningfulOutput drops the framing go test prints around every failure — the
// reader already knows the test failed; they want to know why.
func meaningfulOutput(lines []string) []string {
	var kept []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || t == "FAIL" || t == "PASS" ||
			strings.HasPrefix(t, "--- FAIL:") || strings.HasPrefix(t, "=== RUN") ||
			strings.HasPrefix(t, "=== PAUSE") || strings.HasPrefix(t, "=== CONT") ||
			strings.HasPrefix(t, "--- PASS:") {
			continue
		}
		kept = append(kept, l)
	}
	return kept
}

// HasFailures reports whether the run should fail the job.
func (r *Report) HasFailures() bool {
	return r.empty || len(r.Failures) > 0 || len(r.Noise) > 0
}

// clip shortens one line, keeping its head — that is where the assertion is.
func clip(line string) string {
	if len(line) <= maxLineChars {
		return line
	}
	return line[:maxLineChars] + fmt.Sprintf(" … (+%d chars clipped)", len(line)-maxLineChars)
}

func testCount(n int) string {
	if n == 1 {
		return "1 test"
	}
	return fmt.Sprintf("%d tests", n)
}

func shortPkg(pkg string) string {
	return strings.TrimPrefix(pkg, modulePrefix)
}

func (f Failure) title() string {
	if f.Test == "" {
		return shortPkg(f.Package) + " (package failed before any test ran)"
	}
	return shortPkg(f.Package) + " › " + f.Test
}

func truncate(lines []string) ([]string, int) {
	if len(lines) <= maxOutputLines {
		return lines, 0
	}
	return lines[:maxOutputLines], len(lines) - maxOutputLines
}

// Text renders the report for the CI log.
func (r *Report) Text() string {
	var b strings.Builder

	if r.empty {
		b.WriteString("No test output was produced — the test run died before any test reported.\n")
		b.WriteString("Check the step above for a crashed toolchain, an OOM, or a cancelled job.\n")
		return b.String()
	}

	if !r.HasFailures() {
		fmt.Fprintf(&b, "All %s passed.\n", testCount(r.Passed))
		return b.String()
	}

	fmt.Fprintf(&b, "%d failed, %d passed (%s)\n", r.Failed, r.Passed, testCount(r.Failed+r.Passed))

	for _, f := range r.Failures {
		fmt.Fprintf(&b, "\nFAIL  %s\n", f.title())
		lines, dropped := truncate(f.Output)
		for _, l := range lines {
			fmt.Fprintf(&b, "      %s\n", clip(strings.TrimSpace(l)))
		}
		if dropped > 0 {
			fmt.Fprintf(&b, "      … %d more lines truncated (see the full log above)\n", dropped)
		}
	}

	if len(r.Noise) > 0 {
		b.WriteString("\nFAIL  toolchain errors (outside the test stream)\n")
		lines, dropped := truncate(r.Noise)
		for _, l := range lines {
			fmt.Fprintf(&b, "      %s\n", clip(l))
		}
		if dropped > 0 {
			fmt.Fprintf(&b, "      … %d more lines truncated\n", dropped)
		}
	}

	return b.String()
}

var sourceRef = regexp.MustCompile(`([\w.\-]+_test\.go):(\d+):\s*(.*)`)

// annotationEscape encodes a value for a GitHub workflow command, which is
// line-oriented: a raw newline would end the command.
func annotationEscape(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// Annotations renders GitHub `::error` commands, so failures surface on the run
// summary and inline on the diff instead of only in the log.
func (r *Report) Annotations() []string {
	var out []string
	for _, f := range r.Failures {
		file, line, msg := locate(f)
		var cmd strings.Builder
		cmd.WriteString("::error ")
		if file != "" {
			cmd.WriteString("file=" + file)
			if line != "" {
				cmd.WriteString(",line=" + line)
			}
			cmd.WriteString(",")
		}
		cmd.WriteString("title=" + annotationEscape(f.title()))
		cmd.WriteString("::")
		cmd.WriteString(annotationEscape(msg))
		out = append(out, cmd.String())
	}
	if len(r.Noise) > 0 {
		out = append(out, "::error title="+annotationEscape("Build failed")+"::"+
			annotationEscape(strings.Join(firstN(r.Noise, 10), "\n")))
	}
	if r.empty {
		out = append(out, "::error title=No test output::The test run produced no results — it died before any test reported.")
	}
	return out
}

// locate finds the source position a failure should be pinned to, and the
// message to show there.
func locate(f Failure) (file, line, msg string) {
	var trimmed []string
	for _, l := range firstN(f.Output, maxAnnotationLines) {
		trimmed = append(trimmed, clip(strings.TrimSpace(l)))
	}
	body := strings.Join(trimmed, "\n")
	if len(body) > maxAnnotationChars {
		body = body[:maxAnnotationChars] + " … (truncated — see the log)"
	}
	for _, l := range f.Output {
		m := sourceRef.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		file = m[1]
		if dir := shortPkg(f.Package); dir != "" && !strings.Contains(dir, ".") {
			file = dir + "/" + file
		}
		if _, err := strconv.Atoi(m[2]); err == nil {
			line = m[2]
		}
		break
	}
	if body == "" {
		body = "The test failed without producing output."
	}
	return file, line, body
}

func firstN(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return append(append([]string{}, lines[:n]...), fmt.Sprintf("… %d more lines truncated", len(lines)-n))
}

// Markdown renders the GitHub step summary — the first thing a human sees when
// they open a failed run.
func (r *Report) Markdown(title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", title)

	switch {
	case r.empty:
		b.WriteString("**No test output was produced.** The run died before any test reported —\n")
		b.WriteString("look for a crashed toolchain, an out-of-memory kill, or a cancelled job.\n")
		return b.String()
	case !r.HasFailures():
		fmt.Fprintf(&b, "✅ All **%s** passed.\n", testCount(r.Passed))
		return b.String()
	}

	fmt.Fprintf(&b, "❌ **%d failed**, %d passed (%s)\n", r.Failed, r.Passed, testCount(r.Failed+r.Passed))

	for _, f := range r.Failures {
		fmt.Fprintf(&b, "\n<details open>\n<summary><code>%s</code></summary>\n\n```\n", f.title())
		lines, dropped := truncate(f.Output)
		if len(lines) == 0 {
			b.WriteString("(the test failed without producing output)\n")
		}
		for _, l := range lines {
			b.WriteString(clip(strings.TrimSpace(l)) + "\n")
		}
		if dropped > 0 {
			fmt.Fprintf(&b, "… %d more lines truncated\n", dropped)
		}
		b.WriteString("```\n\n</details>\n")
	}

	if len(r.Noise) > 0 {
		b.WriteString("\n<details open>\n<summary><code>build / toolchain errors</code></summary>\n\n```\n")
		for _, l := range firstN(r.Noise, maxOutputLines) {
			b.WriteString(l + "\n")
		}
		b.WriteString("```\n\n</details>\n")
	}

	return b.String()
}

// Sort keeps failure order stable for readers scanning a long list.
func (r *Report) Sort() {
	sort.SliceStable(r.Failures, func(i, j int) bool {
		if r.Failures[i].Package != r.Failures[j].Package {
			return r.Failures[i].Package < r.Failures[j].Package
		}
		return r.Failures[i].Test < r.Failures[j].Test
	})
}
