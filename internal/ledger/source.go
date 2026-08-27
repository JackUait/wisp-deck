package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Runner executes Git commands relative to a repository.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// InputRunner is implemented by runners that can feed bytes to a Git command.
// Source uses it to batch tracked binary sizes through one cat-file process.
type InputRunner interface {
	RunInput(context.Context, string, []byte, ...string) ([]byte, error)
}

// ExecRunner executes the installed Git binary.
type ExecRunner struct{}

// Run executes git -C dir with the supplied arguments.
func (ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return runGit(ctx, dir, nil, args...)
}

// RunInput executes Git with input connected to stdin.
func (ExecRunner) RunInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	return runGit(ctx, dir, input, args...)
}

func runGit(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}

// Inspector derives untracked-file metadata without spawning one process per
// path.
type Inspector interface {
	Inspect(context.Context, string, string) (Change, error)
}

// InspectFunc adapts a function to Inspector.
type InspectFunc func(context.Context, string, string) (Change, error)

// Inspect calls f.
func (f InspectFunc) Inspect(ctx context.Context, root, path string) (Change, error) {
	return f(ctx, root, path)
}

// SourceOption configures a snapshot source.
type SourceOption func(*Source)

// WithWorkers sets the maximum number of simultaneous file inspections.
func WithWorkers(workers int) SourceOption {
	return func(source *Source) {
		if workers > 0 {
			source.workers = workers
		}
	}
}

// WithInspector replaces untracked-file inspection.
func WithInspector(inspector Inspector) SourceOption {
	return func(source *Source) {
		if inspector != nil {
			source.inspector = inspector
		}
	}
}

// Source builds immutable ledger snapshots from Git and the working tree.
type Source struct {
	runner    Runner
	inspector Inspector
	workers   int
}

// NewSource creates a snapshot source.
func NewSource(runner Runner, options ...SourceOption) *Source {
	source := &Source{
		runner:    runner,
		inspector: InspectFunc(inspectWorktreeFile),
		workers:   4,
	}
	for _, option := range options {
		option(source)
	}
	return source
}

type sourceQuery uint8

const (
	queryStaged sourceQuery = iota
	queryModified
	queryUntracked
	queryBranch
	queryUpstream
)

type sourceQueryResult struct {
	kind sourceQuery
	out  []byte
	err  error
}

// Load gathers one repository generation. Independent Git queries run
// concurrently, and all file inspection observes ctx cancellation.
func (s *Source) Load(ctx context.Context, dir string, generation uint64) (Snapshot, error) {
	queries := []struct {
		kind sourceQuery
		args []string
	}{
		{queryStaged, []string{"diff", "--cached", "--numstat", "-z", "--find-renames"}},
		{queryModified, []string{"diff", "--numstat", "-z", "--find-renames"}},
		{queryUntracked, []string{"ls-files", "--others", "--exclude-standard", "-z"}},
		{queryBranch, []string{"symbolic-ref", "--short", "-q", "HEAD"}},
		{queryUpstream, []string{"rev-list", "--left-right", "--count", "HEAD...@{u}"}},
	}
	results := make(chan sourceQueryResult, len(queries))
	for _, query := range queries {
		query := query
		go func() {
			out, err := s.runner.Run(ctx, dir, query.args...)
			results <- sourceQueryResult{kind: query.kind, out: out, err: err}
		}()
	}

	outputs := make(map[sourceQuery][]byte, len(queries))
	for range queries {
		result := <-results
		if result.err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Snapshot{}, ctxErr
			}
			if result.kind == queryBranch || result.kind == queryUpstream {
				continue
			}
			return Snapshot{}, fmt.Errorf("load ledger query %d: %w", result.kind, result.err)
		}
		outputs[result.kind] = result.out
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	staged, err := parseNumstatZ(outputs[queryStaged], GroupStaged)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse staged changes: %w", err)
	}
	modified, err := parseNumstatZ(outputs[queryModified], GroupModified)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse modified changes: %w", err)
	}
	if err := s.hydrateTrackedBinarySizes(ctx, dir, staged, modified); err != nil {
		return Snapshot{}, fmt.Errorf("load tracked binary sizes: %w", err)
	}
	untrackedPaths, err := parsePathListZ(outputs[queryUntracked])
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse untracked paths: %w", err)
	}
	untracked, err := s.inspectPaths(ctx, dir, untrackedPaths)
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect untracked files: %w", err)
	}

	metadata := Metadata{
		Branch: strings.TrimSpace(string(outputs[queryBranch])),
	}
	if metadata.Branch == "" {
		metadata.Branch = "detached"
	}
	metadata.Ahead, metadata.Behind = parseAheadBehind(outputs[queryUpstream])
	return snapshotFromChanges(generation, staged, modified, untracked, metadata), nil
}

type binarySizeTarget struct {
	change *Change
	old    bool
}

func (s *Source) hydrateTrackedBinarySizes(ctx context.Context, root string, staged, modified []Change) error {
	inputRunner, ok := s.runner.(InputRunner)
	if !ok {
		return nil
	}
	var input []byte
	var targets []binarySizeTarget
	appendRequest := func(spec string, change *Change, old bool) {
		input = append(input, spec...)
		input = append(input, 0)
		targets = append(targets, binarySizeTarget{change: change, old: old})
	}
	for index := range staged {
		change := &staged[index]
		if !change.Binary {
			continue
		}
		oldPath := change.Path
		if change.OldPath != "" {
			oldPath = change.OldPath
		}
		appendRequest("HEAD:"+oldPath, change, true)
		appendRequest(":"+change.Path, change, false)
	}
	for index := range modified {
		change := &modified[index]
		if !change.Binary {
			continue
		}
		oldPath := change.Path
		if change.OldPath != "" {
			oldPath = change.OldPath
		}
		appendRequest(":"+oldPath, change, true)
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(change.Path)))
		if err == nil {
			change.NewBytes = info.Size()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(targets) == 0 {
		return nil
	}

	out, err := inputRunner.RunInput(ctx, root, input,
		"cat-file", "--batch-check=%(objectsize)", "-Z")
	if err != nil {
		return err
	}
	sizes, err := parseBatchSizesZ(out, len(targets))
	if err != nil {
		return err
	}
	for index, target := range targets {
		if target.old {
			target.change.OldBytes = sizes[index]
		} else {
			target.change.NewBytes = sizes[index]
		}
	}
	return nil
}

func parseBatchSizesZ(raw []byte, expected int) ([]int64, error) {
	if len(raw) == 0 || raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("cat-file output is missing its final NUL terminator")
	}
	fields := bytes.Split(raw[:len(raw)-1], []byte{0})
	if len(fields) != expected {
		return nil, fmt.Errorf("cat-file returned %d sizes, want %d", len(fields), expected)
	}
	sizes := make([]int64, len(fields))
	for index, field := range fields {
		size, err := strconv.ParseInt(string(field), 10, 64)
		if err == nil && size >= 0 {
			sizes[index] = size
			continue
		}
		if bytes.HasSuffix(field, []byte(" missing")) {
			continue
		}
		return nil, fmt.Errorf("invalid cat-file size %q", field)
	}
	return sizes, nil
}

func (s *Source) inspectPaths(ctx context.Context, root string, paths []string) ([]Change, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	workers := s.workers
	if workers > len(paths) {
		workers = len(paths)
	}
	type job struct {
		index int
		path  string
	}
	type result struct {
		index  int
		change Change
		err    error
	}
	jobs := make(chan job)
	results := make(chan result, len(paths))
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for item := range jobs {
				change, err := s.inspector.Inspect(ctx, root, item.path)
				results <- result{index: item.index, change: change, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index, path := range paths {
			select {
			case jobs <- job{index: index, path: path}:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(results)
	}()

	changes := make([]Change, len(paths))
	completed := 0
	for item := range results {
		if item.err != nil {
			return nil, item.err
		}
		changes[item.index] = item.change
		completed++
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if completed != len(paths) {
		return nil, fmt.Errorf("inspected %d of %d paths", completed, len(paths))
	}
	return changes, nil
}

// inspectWorktreeFile counts one untracked entry. `git ls-files --others` does
// not list regular files only: it never follows a symlink (so a link to a
// directory arrives as its own entry) and it does not descend into a nested
// repository (which arrives as "<dir>/"). os.Open FOLLOWS symlinks, so opening
// either one yields a directory handle whose Read fails with EISDIR and takes
// the whole snapshot down with it. Only a regular file is ever opened — a FIFO
// would block the read until a writer showed up.
func inspectWorktreeFile(ctx context.Context, root, path string) (Change, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil {
		return Change{}, err
	}
	change := Change{Group: GroupNew, Path: path}
	if info.Mode()&os.ModeSymlink != 0 {
		// git stores a symlink as a blob holding its target path with no
		// trailing newline, so numstat scores every symlink as one added line
		// whether or not the target exists.
		change.Added = 1
		return change, nil
	}
	if !info.Mode().IsRegular() {
		return change, nil
	}

	file, err := os.Open(full)
	if err != nil {
		return Change{}, err
	}
	defer file.Close()

	buffer := make([]byte, 64*1024)
	var size int64
	lines := 0
	last := byte(0)
	for {
		if err := ctx.Err(); err != nil {
			return Change{}, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			if size < 8000 {
				remaining := int64(8000) - size
				limit := int64(n)
				if limit > remaining {
					limit = remaining
				}
				change.Binary = change.Binary || bytes.IndexByte(chunk[:limit], 0) >= 0
			}
			lines += bytes.Count(chunk, []byte{'\n'})
			last = chunk[n-1]
			size += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return Change{}, readErr
		}
	}
	if change.Binary {
		change.NewBytes = size
		return change, nil
	}
	if size > 0 && last != '\n' {
		lines++
	}
	change.Added = lines
	return change, nil
}

func parseAheadBehind(raw []byte) (ahead, behind int) {
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		return 0, 0
	}
	ahead, err := strconv.Atoi(fields[0])
	if err != nil || ahead < 0 {
		return 0, 0
	}
	behind, err = strconv.Atoi(fields[1])
	if err != nil || behind < 0 {
		return 0, 0
	}
	return ahead, behind
}

func snapshotFromChanges(generation uint64, staged, modified, untracked []Change, metadata Metadata) Snapshot {
	rows := make([]Row, 0, len(staged)+len(modified)+len(untracked)+9)
	appendGroup := func(group Group, label string, changes []Change) {
		if len(changes) == 0 {
			return
		}
		rows = append(rows, Row{Kind: RowGroup, Group: group, Label: label, Count: len(changes)})
		for _, change := range changes {
			rows = append(rows, Row{
				Kind:     RowFile,
				ID:       RowID{Group: group, Path: change.Path},
				Path:     change.Path,
				Added:    change.Added,
				Deleted:  change.Deleted,
				Binary:   change.Binary,
				OldBytes: change.OldBytes,
				NewBytes: change.NewBytes,
			})
			metadata.TotalFiles++
			metadata.Added += change.Added
			metadata.Deleted += change.Deleted
		}
		rows = append(rows, Row{Kind: RowSpacer})
	}
	appendGroup(GroupStaged, "staged", staged)
	appendGroup(GroupModified, "modified", modified)
	appendGroup(GroupNew, "new", untracked)
	return NewSnapshot(generation, rows, metadata)
}
