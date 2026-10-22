// Command woma is a git worktree manager: it finds worktrees across your
// repositories, shows what's safe to delete, cleans them up, creates new
// ones and jumps between them.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"woma/internal/config"
)

const version = "0.1.0"

const usage = `woma: git worktree manager

Usage:
  woma [path...]              browse worktrees in a TUI (default command)
  woma pick [path...]         choose a worktree and print its path
  woma new <branch> [flags]   create a worktree for a branch
  woma list [path...] [flags] print worktrees
  woma clean [path...] [flags] delete worktrees matching filters
  woma shell-init [shell]     print the wcd/wnew shell functions
  woma config                 print the config file location and settings
  woma version                print the version

Paths default to the roots in the config file, then the current directory.
Run "woma <command> -h" for a command's flags.
`

// errSilent signals failure without printing anything more.
var errSilent = errors.New("")

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	if err != errSilent && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "woma:", err)
	}
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	os.Exit(1)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cmd, rest := "", args
	if len(args) > 0 {
		cmd, rest = args[0], args[1:]
	}
	switch cmd {
	case "pick":
		return runPick(rest, cfg, stdout, stderr)
	case "new":
		return runNew(rest, cfg, stdout, stderr)
	case "list", "ls":
		return runList(rest, cfg, stdout, stderr)
	case "clean":
		return runClean(rest, cfg, stdin, stdout, stderr)
	case "shell-init":
		return runShellInit(rest, stdout)
	case "config":
		return runConfig(cfg, stdout)
	case "version", "--version":
		fmt.Fprintln(stdout, "woma", version)
		return nil
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	}
	return runBrowse(args, cfg, stdout, stderr)
}

// parseArgs parses flags that may appear before, between or after
// positional arguments, returning the positional ones.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

// resolveRoots turns path arguments into absolute directories, falling back
// to the configured roots and then the current directory.
func resolveRoots(paths []string, cfg config.Config) ([]string, error) {
	if len(paths) == 0 {
		paths = cfg.Roots
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	roots := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(config.ExpandHome(p))
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", abs)
		}
		roots = append(roots, abs)
	}
	return roots, nil
}

func newFlagSet(name, usageLine string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s\n", usageLine)
		fs.PrintDefaults()
	}
	return fs
}
