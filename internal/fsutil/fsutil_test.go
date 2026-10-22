package fsutil

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"woma/internal/testutil"
)

func TestUsageCountsHardLinksOnce(t *testing.T) {
	dir := testutil.TempDir(t)
	testutil.WriteFile(t, filepath.Join(dir, "a"), 256<<10)
	before := Measure(context.Background(), dir).Size
	if before < 256<<10 {
		t.Fatalf("usage %d smaller than file", before)
	}
	if err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "b")); err != nil {
		t.Skip("hard links unsupported:", err)
	}
	if after := Measure(context.Background(), dir).Size; after != before {
		t.Errorf("hard link counted twice: %d -> %d", before, after)
	}
}

func TestRemoveContentsKeepsGitFile(t *testing.T) {
	dir := testutil.TempDir(t)
	os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nowhere\n"), 0o644)
	testutil.WriteFile(t, filepath.Join(dir, "src", "main.go"), 10<<10)
	testutil.WriteFile(t, filepath.Join(dir, "ro", "cache", "blob"), 64<<10)
	os.Chmod(filepath.Join(dir, "ro", "cache", "blob"), 0o444)
	os.Chmod(filepath.Join(dir, "ro", "cache"), 0o555)
	os.Chmod(filepath.Join(dir, "ro"), 0o555)

	if !IsLinkedWorktree(dir) {
		t.Fatal("dir with .git file should be a linked worktree")
	}
	total := Measure(context.Background(), dir).Size
	var freed atomic.Int64
	RemoveContents(dir, &freed)

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != ".git" {
		t.Fatalf("left behind: %v", entries)
	}
	if freed.Load() == 0 || freed.Load() > total {
		t.Errorf("freed %d of %d", freed.Load(), total)
	}
}

func TestIsLinkedWorktreeRejectsMainCheckout(t *testing.T) {
	dir := testutil.TempDir(t)
	os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	if IsLinkedWorktree(dir) {
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
