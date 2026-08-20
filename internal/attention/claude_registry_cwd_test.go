package attention

import (
	"context"
	"testing"
)

// A session follows its agent into a git worktree by watching the record's cwd,
// so that field has to survive the mapper. A parked session still owns its own
// working directory — the job it handed the turn to runs elsewhere and speaks
// only for the status.
func TestClaudeRegistryMapperReportsTheSessionsWorkingDirectory(t *testing.T) {
	t.Parallel()

	t.Run("reports the record's cwd", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive",`+
			`"cwd":"/tmp/project/.claude/worktrees/feature",`+
			`"procStart":"`+registryChildStart+`","status":"busy","updatedAt":100}`)
		got, found, err := registryMapper(configDir, psSnapshot(
			psProcess(100, 1, registryRootStart),
			psProcess(101, 100, registryChildStart),
		)).Poll(context.Background())
		if err != nil || !found {
			t.Fatalf("Poll() found = %v, err = %v", found, err)
		}
		if got.Cwd != "/tmp/project/.claude/worktrees/feature" {
			t.Fatalf("Cwd = %q, want the record's cwd", got.Cwd)
		}
	})

	t.Run("parked session keeps its own working directory", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive",`+
			`"cwd":"/tmp/project","parkedJobId":"59870622",`+
			`"procStart":"`+registryChildStart+`","status":"idle","updatedAt":100}`)
		writeRegistryRecord(t, configDir, 300, `{"pid":300,"kind":"bg","jobId":"59870622",`+
			`"cwd":"/tmp/somewhere-else",`+
			`"procStart":"`+registryRootStart+`","status":"busy","updatedAt":200}`)
		got, found, err := registryMapper(configDir, psSnapshot(
			psProcess(100, 1, registryRootStart),
			psProcess(101, 100, registryChildStart),
			psProcess(300, 1, registryRootStart),
		)).Poll(context.Background())
		if err != nil || !found {
			t.Fatalf("Poll() found = %v, err = %v", found, err)
		}
		if got.Cwd != "/tmp/project" {
			t.Fatalf("Cwd = %q, want the interactive record's cwd", got.Cwd)
		}
		if got.Status != "busy" {
			t.Fatalf("Status = %q, want the job's status", got.Status)
		}
	})

	t.Run("absent cwd is an ordinary answer", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive",`+
			`"procStart":"`+registryChildStart+`","status":"idle","updatedAt":100}`)
		got, found, err := registryMapper(configDir, psSnapshot(
			psProcess(100, 1, registryRootStart),
			psProcess(101, 100, registryChildStart),
		)).Poll(context.Background())
		if err != nil || !found {
			t.Fatalf("Poll() found = %v, err = %v", found, err)
		}
		if got.Cwd != "" {
			t.Fatalf("Cwd = %q, want empty", got.Cwd)
		}
	})

	// The cwd is handed to a shell that respawns panes into it, so a record
	// carrying anything but a plain absolute path is rejected outright rather
	// than sanitized — the same fail-closed treatment the identifiers get.
	t.Run("a cwd that is not a plain absolute path rejects the record", func(t *testing.T) {
		t.Parallel()
		for name, cwd := range map[string]string{
			"relative":         `"project/sub"`,
			"control char":     "\"/tmp/pro\\u0007ject\"",
			"not a string":     `42`,
			"embedded newline": "\"/tmp/pro\\nject\"",
			"empty":            `""`,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				configDir := t.TempDir()
				writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive",`+
					`"cwd":`+cwd+`,`+
					`"procStart":"`+registryChildStart+`","status":"idle","updatedAt":100}`)
				_, found, err := registryMapper(configDir, psSnapshot(
					psProcess(100, 1, registryRootStart),
					psProcess(101, 100, registryChildStart),
				)).Poll(context.Background())
				if err != nil {
					t.Fatalf("Poll() error = %v", err)
				}
				if found {
					t.Fatal("Poll() found = true, want the record rejected")
				}
			})
		}
	})
}
