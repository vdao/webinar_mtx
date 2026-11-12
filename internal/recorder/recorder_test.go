package recorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestWorker() *Worker {
	return &Worker{
		fileSigs: map[string]map[string]time.Time{},
		offline:  map[string]time.Time{},
		lastFail: map[string]time.Time{},
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStableFiles(t *testing.T) {
	dir := t.TempDir()
	w := newTestWorker()
	seg := filepath.Join(dir, "2026-11-12_10-00-00.mp4")
	write(t, seg)
	other := filepath.Join(dir, "notes.txt")
	write(t, other)

	// First tick: snapshot taken, not ready yet.
	files, ready := w.stableFiles("w1", dir)
	if ready || files != nil {
		t.Fatalf("first tick: ready=%v files=%v, want false/nil", ready, files)
	}
	// Second tick without changes: stable.
	files, ready = w.stableFiles("w1", dir)
	if !ready || len(files) != 1 || filepath.Base(files[0]) != "2026-11-12_10-00-00.mp4" {
		t.Fatalf("second tick: ready=%v files=%v", ready, files)
	}
	// A new mtime (still recording) resets stability.
	future := time.Now().Add(time.Hour)
	os.Chtimes(seg, future, future)
	if _, ready = w.stableFiles("w1", dir); ready {
		t.Fatal("changed file must not be stable")
	}
	// ...and settles again on the next identical tick.
	if _, ready = w.stableFiles("w1", dir); !ready {
		t.Fatal("settled snapshot must be stable")
	}
}

func TestStableFilesMissingDir(t *testing.T) {
	w := newTestWorker()
	if files, ready := w.stableFiles("w2", filepath.Join(t.TempDir(), "nope")); ready || files != nil {
		t.Fatalf("missing dir: ready=%v files=%v", ready, files)
	}
}

func TestStableFilesEmptyDir(t *testing.T) {
	w := newTestWorker()
	if files, ready := w.stableFiles("w3", t.TempDir()); ready || files != nil {
		t.Fatalf("empty dir: ready=%v files=%v", ready, files)
	}
}

func TestSentinelErrors(t *testing.T) {
	if !IsNoFiles(errNoFiles) || !IsNotStable(errNotStable) {
		t.Fatal("sentinels must match themselves")
	}
	if IsNoFiles(errNotStable) || IsNotStable(errNoFiles) {
		t.Fatal("sentinels must not match each other")
	}
}
