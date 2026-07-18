package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestHostProcessAncestryDetectsStructuralSignals(t *testing.T) {
	longTestExecutable := "/private/tmp/" + strings.Repeat("long-name-", 8) + "runner.test"
	tests := map[string]struct {
		start int
		infos map[int]hostProcessInfo
		want  hostProcessAncestry
	}{
		"exact marker in parent environment": {
			start: 40,
			infos: map[int]hostProcessInfo{
				40: {ParentPID: 30, Executable: "/bin/zsh"},
				30: {
					ParentPID:  1,
					Executable: "/usr/local/bin/node",
					Environment: []string{
						"WISP_DECK_TESTING=1",
					},
				},
				1: {ParentPID: 0},
			},
			want: hostProcessAncestry{TestMarker: true},
		},
		"marker prefix is not exact": {
			start: 40,
			infos: map[int]hostProcessInfo{
				40: {
					ParentPID:  1,
					Executable: "/bin/zsh",
					Environment: []string{
						"WISP_DECK_TESTING=10",
					},
				},
				1: {ParentPID: 0},
			},
			want: hostProcessAncestry{Known: true},
		},
		"long full test executable": {
			start: 40,
			infos: map[int]hostProcessInfo{
				40: {ParentPID: 20, Executable: "/bin/zsh"},
				20: {ParentPID: 1, Executable: longTestExecutable},
				1:  {ParentPID: 0},
			},
			want: hostProcessAncestry{Known: true, TestExecutable: true},
		},
		"farther marker beats nearer test executable": {
			start: 40,
			infos: map[int]hostProcessInfo{
				40: {ParentPID: 30, Executable: "/bin/zsh"},
				30: {ParentPID: 20, Executable: longTestExecutable},
				20: {
					ParentPID:  1,
					Executable: "/tmp/renamed-parent",
					Environment: []string{
						"WISP_DECK_TESTING=1",
					},
				},
				1: {ParentPID: 0},
			},
			want: hostProcessAncestry{
				TestMarker:     true,
				TestExecutable: true,
			},
		},
		"normal shell and node ancestry": {
			start: 40,
			infos: map[int]hostProcessInfo{
				40: {ParentPID: 30, Executable: "/bin/zsh"},
				30: {ParentPID: 1, Executable: "/opt/homebrew/bin/node"},
				1:  {ParentPID: 0},
			},
			want: hostProcessAncestry{Known: true},
		},
		"renamed test uses marker": {
			start: 40,
			infos: map[int]hostProcessInfo{
				40: {ParentPID: 20, Executable: "/tmp/renamed-worker"},
				20: {
					ParentPID:  1,
					Executable: "/tmp/also-renamed",
					Environment: []string{
						"WISP_DECK_TESTING=1",
					},
				},
				1: {ParentPID: 0},
			},
			want: hostProcessAncestry{TestMarker: true},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := inspectHostProcessAncestry(test.start, func(pid int) (hostProcessInfo, error) {
				info, ok := test.infos[pid]
				if !ok {
					return hostProcessInfo{}, fmt.Errorf("unexpected PID %d", pid)
				}
				return info, nil
			})
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("inspectHostProcessAncestry() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestHostProcessAncestryFailsClosedWhenTraversalIsUntrusted(t *testing.T) {
	t.Run("lookup error", func(t *testing.T) {
		got := inspectHostProcessAncestry(22, func(int) (hostProcessInfo, error) {
			return hostProcessInfo{}, errors.New("unavailable")
		})
		if got.Known {
			t.Fatalf("lookup error returned known ancestry: %#v", got)
		}
	})

	t.Run("confirmed marker survives later lookup error", func(t *testing.T) {
		got := inspectHostProcessAncestry(22, func(pid int) (hostProcessInfo, error) {
			switch pid {
			case 22:
				return hostProcessInfo{
					ParentPID:  21,
					Executable: "/tmp/renamed-worker",
					Environment: []string{
						"WISP_DECK_TESTING=1",
					},
				}, nil
			default:
				return hostProcessInfo{}, errors.New("unavailable")
			}
		})
		if !got.TestMarker {
			t.Fatalf("confirmed marker was erased by later lookup failure: %#v", got)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		infos := map[int]hostProcessInfo{
			22: {ParentPID: 21, Executable: "/bin/zsh"},
			21: {ParentPID: 22, Executable: "/usr/bin/node"},
		}
		got := inspectHostProcessAncestry(22, func(pid int) (hostProcessInfo, error) {
			return infos[pid], nil
		})
		if got.Known {
			t.Fatalf("cycle returned known ancestry: %#v", got)
		}
	})

	t.Run("invalid root", func(t *testing.T) {
		got := inspectHostProcessAncestry(22, func(int) (hostProcessInfo, error) {
			return hostProcessInfo{ParentPID: 0, Executable: "/bin/zsh"}, nil
		})
		if got.Known {
			t.Fatalf("non-PID-1 root returned known ancestry: %#v", got)
		}
	})

	t.Run("malformed parent", func(t *testing.T) {
		got := inspectHostProcessAncestry(22, func(int) (hostProcessInfo, error) {
			return hostProcessInfo{ParentPID: -1, Executable: "/bin/zsh"}, nil
		})
		if got.Known {
			t.Fatalf("negative parent returned known ancestry: %#v", got)
		}
	})

	t.Run("hard traversal bound", func(t *testing.T) {
		calls := 0
		got := inspectHostProcessAncestry(
			hostProcessTraversalLimit+10,
			func(pid int) (hostProcessInfo, error) {
				calls++
				return hostProcessInfo{
					ParentPID:  pid - 1,
					Executable: "/bin/zsh",
				}, nil
			},
		)
		if got.Known {
			t.Fatalf("overlong ancestry returned known: %#v", got)
		}
		if calls != hostProcessTraversalLimit {
			t.Fatalf("lookup calls = %d, want hard bound %d", calls, hostProcessTraversalLimit)
		}
	})
}

func TestHostProcessAncestryTreatsPID1AsValidatedRoot(t *testing.T) {
	var lookedUp []int
	got := inspectHostProcessAncestry(2, func(pid int) (hostProcessInfo, error) {
		lookedUp = append(lookedUp, pid)
		switch pid {
		case 2:
			return hostProcessInfo{ParentPID: 1, Executable: "/bin/zsh"}, nil
		case 1:
			return hostProcessInfo{ParentPID: 0}, nil
		default:
			return hostProcessInfo{}, fmt.Errorf("unexpected PID %d", pid)
		}
	})
	if !reflect.DeepEqual(got, hostProcessAncestry{Known: true}) {
		t.Fatalf("inspectHostProcessAncestry() = %#v, want validated root", got)
	}
	if !reflect.DeepEqual(lookedUp, []int{2, 1}) {
		t.Fatalf("lookups = %v, want [2 1]", lookedUp)
	}
}

func TestHostProcessAncestryRawProcArgsSeparatesArgumentsAndEnvironment(t *testing.T) {
	executable := "/private/tmp/" + strings.Repeat("very-long-component-", 5) + "worker"
	raw := kernProcArgsFixture(
		executable,
		[]string{"renamed-worker", "WISP_DECK_TESTING=1", "--flag"},
		[]string{"HOME=/tmp/home", "WISP_DECK_TESTING=10", "PATH=/usr/bin"},
	)

	gotExecutable, gotEnvironment, err := parseKernProcArgs2(raw)
	if err != nil {
		t.Fatalf("parseKernProcArgs2: %v", err)
	}
	if gotExecutable != executable {
		t.Fatalf("executable = %q, want exact full path %q", gotExecutable, executable)
	}
	wantEnvironment := []string{
		"HOME=/tmp/home",
		"WISP_DECK_TESTING=10",
		"PATH=/usr/bin",
	}
	if !reflect.DeepEqual(gotEnvironment, wantEnvironment) {
		t.Fatalf("environment = %v, want %v", gotEnvironment, wantEnvironment)
	}
	if hostEnvironmentHasTestMarker(gotEnvironment) {
		t.Fatal("marker present only in argv or as a prefix was treated as exact environment")
	}

	exactRaw := kernProcArgsFixture(
		"/usr/bin/node",
		[]string{"node", "script.js"},
		[]string{"WISP_DECK_TESTING=1"},
	)
	_, exactEnvironment, err := parseKernProcArgs2(exactRaw)
	if err != nil {
		t.Fatalf("parse exact fixture: %v", err)
	}
	if !hostEnvironmentHasTestMarker(exactEnvironment) {
		t.Fatal("exact environment marker was not recovered")
	}

	zeroEnvironmentRaw := kernProcArgsFixture(
		"/bin/zsh",
		[]string{"zsh", "-c", "true"},
		nil,
	)
	_, zeroEnvironment, err := parseKernProcArgs2(zeroEnvironmentRaw)
	if err != nil {
		t.Fatalf("parse valid zero-environment fixture: %v", err)
	}
	if len(zeroEnvironment) != 0 {
		t.Fatalf("zero environment fixture returned %v", zeroEnvironment)
	}
}

func TestHostProcessAncestryRawProcArgsRejectsMalformedRecords(t *testing.T) {
	valid := kernProcArgsFixture("/bin/zsh", []string{"zsh", "-c", "true"}, []string{"HOME=/tmp"})
	tests := map[string][]byte{
		"truncated argc": {1, 2, 3},
		"missing executable NUL": func() []byte {
			raw := make([]byte, 4)
			binary.NativeEndian.PutUint32(raw, 1)
			return append(raw, []byte("/bin/zsh")...)
		}(),
		"impossible zero argc": func() []byte {
			raw := append([]byte(nil), valid...)
			binary.NativeEndian.PutUint32(raw[:4], 0)
			return raw
		}(),
		"negative signed argc": func() []byte {
			raw := append([]byte(nil), valid...)
			binary.NativeEndian.PutUint32(raw[:4], ^uint32(0))
			return raw
		}(),
		"impossible huge positive argc": func() []byte {
			raw := append([]byte(nil), valid...)
			binary.NativeEndian.PutUint32(raw[:4], uint32(^uint32(0)>>1))
			return raw
		}(),
		"missing argv NUL": func() []byte {
			raw := kernProcArgsFixture("/bin/zsh", []string{"zsh"}, nil)
			return raw[:len(raw)-1]
		}(),
		"unterminated environment": func() []byte {
			raw := kernProcArgsFixture(
				"/bin/zsh",
				[]string{"zsh"},
				[]string{"HOME=/tmp"},
			)
			return raw[:len(raw)-1]
		}(),
		"malformed trailing bytes": append(
			kernProcArgsFixture("/bin/zsh", []string{"zsh"}, nil),
			[]byte("not-an-environment-record")...,
		),
		"truncated argv count": kernProcArgsFixture(
			"/bin/zsh",
			[]string{"zsh"},
			nil,
			2,
		),
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseKernProcArgs2(raw); err == nil {
				t.Fatal("parseKernProcArgs2 accepted malformed record")
			}
		})
	}
}

func kernProcArgsFixture(
	executable string,
	argv []string,
	environment []string,
	argcOverride ...int,
) []byte {
	argc := len(argv)
	if len(argcOverride) > 0 {
		argc = argcOverride[0]
	}
	raw := make([]byte, 4)
	binary.NativeEndian.PutUint32(raw, uint32(argc))
	raw = append(raw, executable...)
	raw = append(raw, 0, 0, 0)
	for _, argument := range argv {
		raw = append(raw, argument...)
		raw = append(raw, 0)
	}
	for _, entry := range environment {
		raw = append(raw, entry...)
		raw = append(raw, 0)
	}
	return raw
}

func TestHostProcessLookupDarwinLiveHelper(t *testing.T) {
	if os.Getenv("WISP_DECK_HOST_LOOKUP_HELPER") != "1" {
		return
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestHostProcessLookupDarwinReadsInitialFullExecutableAndEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("KERN_PROCARGS2 is Darwin-only")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestHostProcessLookupDarwinLiveHelper$")
	cmd.Env = append(
		environmentWithoutKey(os.Environ(), wispDeckTestingEnvironment),
		"WISP_DECK_HOST_LOOKUP_HELPER=1",
		wispDeckTestingEnvironment+"=1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("wait for helper: %v", err)
	}
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper handshake = %q, want ready", line)
	}

	info, err := lookupHostProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("lookupHostProcess: %v", err)
	}
	resolvedExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	resolvedInfoExecutable, err := filepath.EvalSymlinks(info.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedInfoExecutable != resolvedExecutable {
		t.Fatalf("executable = %q, want exact %q", info.Executable, executable)
	}
	if !hostEnvironmentHasTestMarker(info.Environment) {
		t.Fatalf("initial environment marker missing from %v", info.Environment)
	}
}

func TestHostProcessLookupDarwinPID1AvoidsProtectedProcArgs(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin process lookup only")
	}
	info, err := lookupHostProcess(1)
	if err != nil {
		t.Fatalf("lookupHostProcess(1): %v", err)
	}
	if info.ParentPID != 0 {
		t.Fatalf("PID 1 parent = %d, want 0", info.ParentPID)
	}
	if info.Executable != "" || len(info.Environment) != 0 {
		t.Fatalf("PID 1 unexpectedly read protected procargs: %#v", info)
	}
}

func environmentWithoutKey(environment []string, key string) []string {
	filtered := make([]string, 0, len(environment))
	prefix := key + "="
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
