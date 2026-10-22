package fsutil

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carlosrberto/woma/internal/testutil"
)

func TestMeasureHardLinks(t *testing.T) {
	dir := testutil.TempDir(t)
	testutil.WriteFile(t, filepath.Join(dir, "a"), 256<<10)
	before := Measure(context.Background(), dir).Size
	if before < 256<<10 {
		t.Fatalf("size %d smaller than file", before)
	}
	if err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "b")); err != nil {
		t.Skip("hard links unsupported:", err)
	}
	if after := Measure(context.Background(), dir).Size; after != before {
		t.Errorf("hard link inside the tree counted twice: %d -> %d", before, after)
	}

	// A file also linked from outside (like a package manager's store)
	// frees nothing when the tree is deleted.
	store := testutil.TempDir(t)
	testutil.WriteFile(t, filepath.Join(store, "shared"), 512<<10)
	os.Link(filepath.Join(store, "shared"), filepath.Join(dir, "shared"))
	if got := Measure(context.Background(), dir).Size; got != before {
		t.Errorf("externally linked file counted: %d -> %d", before, got)
	}
	var freed atomic.Int64
	if err := RemoveTree(dir, &freed); err != nil {
		t.Fatal(err)
	}
	if freed.Load() >= before+512<<10 {
		t.Errorf("freed %d counts the externally linked file", freed.Load())
	}
	if _, err := os.Stat(filepath.Join(store, "shared")); err != nil {
		t.Error("external link target must survive")
	}
}

func TestRemoveTree(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "wt")
	testutil.WriteFile(t, filepath.Join(dir, ".git"), 10)
	testutil.WriteFile(t, filepath.Join(dir, "src", "main.go"), 10<<10)
	testutil.WriteFile(t, filepath.Join(dir, "ro", "cache", "blob"), 64<<10)
	os.Chmod(filepath.Join(dir, "ro", "cache", "blob"), 0o444)
	os.Chmod(filepath.Join(dir, "ro", "cache"), 0o555)
	os.Chmod(filepath.Join(dir, "ro"), 0o555)

	total := Measure(context.Background(), dir).Size
	var freed atomic.Int64
	if err := RemoveTree(dir, &freed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("directory still exists")
	}
	if freed.Load() != total {
		t.Errorf("freed %d, measured %d", freed.Load(), total)
	}
}

func TestIsLinkedWorktree(t *testing.T) {
	dir := testutil.TempDir(t)
	os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /x\n"), 0o644)
	if !IsLinkedWorktree(dir) {
		t.Fatal("dir with .git file should be a linked worktree")
	}
	main := testutil.TempDir(t)
	os.Mkdir(filepath.Join(main, ".git"), 0o755)
	if IsLinkedWorktree(main) {
		t.Fatal(".git directory must not count as a linked worktree")
	}
	if IsLinkedWorktree(filepath.Join(dir, "missing")) {
		t.Fatal("missing dir must not count as a linked worktree")
	}
}

func TestMeasureNewest(t *testing.T) {
	dir := testutil.TempDir(t)
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-time.Hour).Truncate(time.Second)
	testutil.WriteFile(t, filepath.Join(dir, "a"), 10)
	testutil.WriteFile(t, filepath.Join(dir, "sub", "b"), 10)
	testutil.WriteFile(t, filepath.Join(dir, ".git"), 10) // ignored: git touches it
	os.Chtimes(filepath.Join(dir, "a"), old, old)
	os.Chtimes(filepath.Join(dir, "sub", "b"), recent, recent)
	if got := Measure(context.Background(), dir).Newest; !got.Equal(recent) {
		t.Errorf("Newest = %v, want %v", got, recent)
	}
}
