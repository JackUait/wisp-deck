package codexadapter

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Codex 0.160.1 answers `--listen unix://<path>` by binding its socket under
// its own /tmp/codex-daemon-<uid>/ and leaving a symlink at <path>.
func listenUnix(t *testing.T, path string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(shortCodexTempBase(t), "d")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWaitForCodexSocket_acceptsASymlinkToAPrivateSocket(t *testing.T) {
	target := filepath.Join(privateDir(t), "s")
	listenUnix(t, target)
	link := filepath.Join(privateDir(t), "a.sock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForCodexSocket(ctx, link, 5*time.Millisecond); err != nil {
		t.Fatalf("wait = %v, want nil", err)
	}
}

func TestWaitForCodexSocket_waitsForASymlinkWhoseSocketIsNotBoundYet(t *testing.T) {
	target := filepath.Join(privateDir(t), "s")
	link := filepath.Join(privateDir(t), "a.sock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		listener, err := net.Listen("unix", target)
		if err == nil {
			t.Cleanup(func() { listener.Close() })
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForCodexSocket(ctx, link, 5*time.Millisecond); err != nil {
		t.Fatalf("wait = %v, want nil", err)
	}
}

func TestWaitForCodexSocket_rejectsASymlinkToANonSocket(t *testing.T) {
	target := filepath.Join(privateDir(t), "f")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(privateDir(t), "a.sock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForCodexSocket(ctx, link, 5*time.Millisecond); err == nil {
		t.Fatal("wait = nil, want an error for a symlink to a regular file")
	}
}

func TestWaitForCodexSocket_rejectsASymlinkIntoASharedDirectory(t *testing.T) {
	shared := privateDir(t)
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(shared, "s")
	listenUnix(t, target)
	link := filepath.Join(privateDir(t), "a.sock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForCodexSocket(ctx, link, 5*time.Millisecond); err == nil {
		t.Fatal("wait = nil, want an error: anyone can replace a socket in a world-writable directory")
	}
}
