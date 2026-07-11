// Command ci-report turns a `go test -json` stream into a failure report a
// human can act on: which test broke, where, and what it printed.
//
// Usage:
//
//	go test -json ./... 2>&1 | tee out.json
//	go run ./cmd/ci-report --title "Bash tests" out.json
//
// It prints the report to stdout, emits GitHub `::error` annotations, writes a
// markdown summary to $GITHUB_STEP_SUMMARY when set, and exits 1 if anything
// failed — so it replaces both the "summarize" and the "did it pass?" steps.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jackuait/wisp-deck/internal/cireport"
)

func main() {
	title := flag.String("title", "Test results", "heading for the GitHub step summary")
	flag.Parse()

	in := os.Stdin
	if path := flag.Arg(0); path != "" {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ci-report: could not read test output %q: %v\n", path, err)
			fmt.Fprintln(os.Stderr, "The test step produced no test output — it likely crashed before running any test.")
			fmt.Println("::error title=No test output::The test step produced no output file; it crashed before any test ran.")
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}

	rep := cireport.Parse(in)
	rep.Sort()

	fmt.Print(rep.Text())

	for _, a := range rep.Annotations() {
		fmt.Println(a)
	}

	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		// Append: the summary file is shared by every step in the job.
		f, err := os.OpenFile(summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, err = f.WriteString(rep.Markdown(*title))
			f.Close()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "ci-report: could not write step summary: %v\n", err)
		}
	}

	if rep.HasFailures() {
		os.Exit(1)
	}
}
