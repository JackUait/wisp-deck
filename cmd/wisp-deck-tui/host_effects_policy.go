package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// HostEffectsCapability is stamped "enabled" only by trusted production build
// entrypoints. Ordinary go build and every unknown linker value fail closed.
var HostEffectsCapability = "disabled"

const HostEffectsBoundaryVersion = 1
const wispDeckTestingEnvironment = "WISP_DECK_TESTING"
const wispDeckRepositoryTestSentinel = "__WISP_DECK_REPOSITORY_TEST_V1__.test"

const (
	hostEffectsDeniedCompiled           = "compiled_disabled"
	hostEffectsDeniedGoTest             = "go_test_binary"
	hostEffectsDeniedCurrentMarker      = "current_test_marker"
	hostEffectsDeniedAncestorSentinel   = "test_ancestor_sentinel"
	hostEffectsDeniedAncestorMarker     = "test_ancestor_marker"
	hostEffectsDeniedAncestorTestBinary = "test_ancestor_binary"
	hostEffectsDeniedUnknownAncestry    = "unknown_ancestry"
	hostProcessTraversalLimit           = 128
)

type hostEffectsDecision struct {
	Allowed      bool
	DenialReason string
}

func hostEffectsAllowed(
	capability string,
	testBinary bool,
	testEnvironment string,
	testAncestor bool,
	ancestryKnown bool,
) bool {
	return capability == "enabled" &&
		!testBinary &&
		testEnvironment != "1" &&
		!testAncestor &&
		ancestryKnown
}

func hostEffectsDecisionFor(
	capability string,
	testBinary bool,
	testEnvironment string,
	ancestry hostProcessAncestry,
) hostEffectsDecision {
	allowed := hostEffectsAllowed(
		capability,
		testBinary,
		testEnvironment,
		ancestry.TestSentinel || ancestry.TestMarker || ancestry.TestExecutable,
		ancestry.Known,
	)
	if allowed {
		return hostEffectsDecision{Allowed: true}
	}

	switch {
	case capability != "enabled":
		return hostEffectsDecision{DenialReason: hostEffectsDeniedCompiled}
	case testBinary:
		return hostEffectsDecision{DenialReason: hostEffectsDeniedGoTest}
	case testEnvironment == "1":
		return hostEffectsDecision{DenialReason: hostEffectsDeniedCurrentMarker}
	case ancestry.TestSentinel:
		return hostEffectsDecision{DenialReason: hostEffectsDeniedAncestorSentinel}
	case ancestry.TestMarker:
		return hostEffectsDecision{DenialReason: hostEffectsDeniedAncestorMarker}
	case ancestry.TestExecutable:
		return hostEffectsDecision{DenialReason: hostEffectsDeniedAncestorTestBinary}
	default:
		return hostEffectsDecision{DenialReason: hostEffectsDeniedUnknownAncestry}
	}
}

// currentHostEffectsDecision is the sole runtime policy for repository-owned
// host effects. Cheap in-process denials short-circuit before process ancestry
// is inspected.
func currentHostEffectsDecision() hostEffectsDecision {
	capability := HostEffectsCapability
	testBinary := testing.Testing()
	testEnvironment := os.Getenv(wispDeckTestingEnvironment)
	if capability != "enabled" || testBinary || testEnvironment == "1" {
		return hostEffectsDecisionFor(
			capability,
			testBinary,
			testEnvironment,
			hostProcessAncestry{},
		)
	}

	ancestry := inspectHostProcessAncestry(os.Getpid(), lookupHostProcess)
	return hostEffectsDecisionFor(
		capability,
		testBinary,
		testEnvironment,
		ancestry,
	)
}

type hostProcessInfo struct {
	ParentPID   int
	Executable  string
	Arguments   []string
	Environment []string
}

type hostProcessLookup func(int) (hostProcessInfo, error)

type hostProcessAncestry struct {
	Known          bool
	TestSentinel   bool
	TestMarker     bool
	TestExecutable bool
}

func inspectHostProcessAncestry(
	startPID int,
	lookup hostProcessLookup,
) hostProcessAncestry {
	if startPID < 1 || lookup == nil {
		return hostProcessAncestry{}
	}

	result := hostProcessAncestry{}
	visited := make(map[int]struct{}, hostProcessTraversalLimit)
	pid := startPID
	for range hostProcessTraversalLimit {
		if pid < 1 {
			return hostProcessAncestry{}
		}
		if _, exists := visited[pid]; exists {
			return hostProcessAncestry{}
		}
		visited[pid] = struct{}{}

		info, err := lookup(pid)
		if err != nil {
			return hostProcessAncestry{}
		}
		if hostArgumentsHaveTestSentinel(info.Arguments) {
			result.TestSentinel = true
			return result
		}
		if hostEnvironmentHasTestMarker(info.Environment) {
			result.TestMarker = true
			return result
		}
		if strings.HasSuffix(filepath.Base(info.Executable), ".test") {
			result.TestExecutable = true
		}

		if pid == 1 {
			if info.ParentPID != 0 {
				return hostProcessAncestry{}
			}
			result.Known = true
			return result
		}
		if info.ParentPID < 1 || info.ParentPID == pid {
			return hostProcessAncestry{}
		}
		pid = info.ParentPID
	}
	return hostProcessAncestry{}
}

func hostArgumentsHaveTestSentinel(arguments []string) bool {
	return len(arguments) > 0 &&
		arguments[0] == wispDeckRepositoryTestSentinel
}

func hostEnvironmentHasTestMarker(environment []string) bool {
	const marker = wispDeckTestingEnvironment + "=1"
	for _, entry := range environment {
		if entry == marker {
			return true
		}
	}
	return false
}

// parseKernProcArgs2 decodes Darwin's KERN_PROCARGS2 record without relying on
// the truncated P_comm field. argc is a signed native-endian int32, followed by
// the executable, NUL padding, exactly argc argument strings, and environment.
func parseKernProcArgs2(raw []byte) (string, []string, []string, error) {
	const argcBytes = 4
	if len(raw) < argcBytes {
		return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 argc is truncated")
	}
	argc32 := int32(binary.NativeEndian.Uint32(raw[:argcBytes]))
	if argc32 <= 0 {
		return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 argc is invalid: %d", argc32)
	}
	argc := int(argc32)
	if argc > len(raw)-argcBytes {
		return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 argc is impossible: %d", argc)
	}

	offset := argcBytes
	executableEnd := bytes.IndexByte(raw[offset:], 0)
	if executableEnd <= 0 {
		return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 executable is malformed")
	}
	executable := string(raw[offset : offset+executableEnd])
	offset += executableEnd + 1
	for offset < len(raw) && raw[offset] == 0 {
		offset++
	}
	if offset >= len(raw) {
		return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 argv is missing")
	}

	arguments := make([]string, 0, argc)
	for argument := 0; argument < argc; argument++ {
		end := bytes.IndexByte(raw[offset:], 0)
		if end < 0 {
			return "", nil, nil, fmt.Errorf(
				"KERN_PROCARGS2 argv[%d] is unterminated",
				argument,
			)
		}
		if argument == 0 && end == 0 {
			return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 argv[0] is empty")
		}
		arguments = append(arguments, string(raw[offset:offset+end]))
		offset += end + 1
		if offset > len(raw) {
			return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 argv is truncated")
		}
	}

	var environment []string
	for offset < len(raw) {
		if raw[offset] == 0 {
			offset++
			continue
		}
		end := bytes.IndexByte(raw[offset:], 0)
		if end < 0 {
			return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 environment is unterminated")
		}
		entry := string(raw[offset : offset+end])
		if !strings.Contains(entry, "=") {
			return "", nil, nil, fmt.Errorf("KERN_PROCARGS2 environment entry is malformed")
		}
		environment = append(environment, entry)
		offset += end + 1
	}

	return executable, arguments, environment, nil
}
