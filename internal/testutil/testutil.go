// Package testutil builds throwaway git repositories for tests. Everything is
// created under t.TempDir() and git runs without the user's global or system
// config, so tests never touch real repositories or settings.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// IsolateGit makes every git process started by the test binary (including
// the code under test) ignore global/system config. Call it from TestMain.
func IsolateGit() {
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		os.Setenv(k, v)
	}
}

// TempDir returns a fresh temp dir with symlinks resolved (macOS /var ->
// /private/var), so paths match what git reports.
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Git runs git in dir and fails the test on error.
func Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// NewRepo creates a repository with one empty commit at dir.
func NewRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
}

// WriteFile creates a file of the given size, creating parent directories.
func WriteFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit writes file (relative to dir) and commits it with message msg.
func Commit(t *testing.T, dir, file, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(msg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "add", file)
	Git(t, dir, "commit", "-q", "-m", msg)
}

// NewRepoWithRemote creates parent/name with an "origin" remote (a bare repo
// at parent/name-remote.git) that has main pushed and origin/HEAD set.
func NewRepoWithRemote(t *testing.T, parent, name string) string {
	t.Helper()
	remote := filepath.Join(parent, name+"-remote.git")
	Git(t, parent, "init", "-q", "--bare", "-b", "main", remote)
	dir := filepath.Join(parent, name)
	NewRepo(t, dir)
	Git(t, dir, "remote", "add", "origin", remote)
	Git(t, dir, "push", "-q", "-u", "origin", "main")
	Git(t, dir, "remote", "set-head", "origin", "main")
	return dir
}
