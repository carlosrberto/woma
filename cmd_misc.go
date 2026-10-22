package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/carlosrberto/woma/internal/config"
	"github.com/carlosrberto/woma/internal/core"
)

func runNew(args []string, cfg config.Config, stdout, stderr io.Writer) error {
	fs := newFlagSet("new", "woma new <branch> [--base ref] [--path dir] [--no-copy] [--no-install] [-C dir]", stderr)
	base := fs.String("base", "", "create the branch from this ref (default: current HEAD)")
	path := fs.String("path", "", "where to create the worktree (default: worktree_dir from the config)")
	noCopy := fs.Bool("no-copy", false, "don't copy env files from the main worktree")
	noInstall := fs.Bool("no-install", false, "don't install dependencies")
	dir := fs.String("C", ".", "run as if started in this directory")
	positional, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one branch name")
	}
	created, err := core.CreateWorktree(context.Background(), *dir, cfg, core.CreateOptions{
		Branch: positional[0], Base: *base, Path: *path,
		NoCopy: *noCopy, NoInstall: *noInstall, Log: stderr,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, created)
	return nil
}

func runShellInit(args []string, stdout io.Writer) error {
	shell := filepath.Base(os.Getenv("SHELL"))
	if len(args) > 0 {
		shell = args[0]
	}
	switch shell {
	case "fish":
		fmt.Fprint(stdout, `function wcd --description 'cd into a worktree picked with woma'
    set -l dir (command woma pick $argv); and test -n "$dir"; and cd $dir
end
function wnew --description 'create a worktree with woma and cd into it'
    set -l dir (command woma new $argv); and test -n "$dir"; and cd $dir
end
`)
	case "zsh", "bash", "sh", "":
		fmt.Fprint(stdout, `# cd into a worktree picked with woma
wcd() { local dir; dir="$(command woma pick "$@")" && [ -n "$dir" ] && cd -- "$dir"; }
# create a worktree with woma and cd into it
wnew() { local dir; dir="$(command woma new "$@")" && [ -n "$dir" ] && cd -- "$dir"; }
`)
	default:
		return fmt.Errorf("unsupported shell %q (use zsh, bash or fish)", shell)
	}
	return nil
}

func runConfig(stdout io.Writer) error {
	path := config.Path()
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stdout, "# %s (invalid)\n", path)
		return err
	}
	state := "not found, using defaults"
	if _, err := os.Stat(path); err == nil {
		state = "loaded"
	}
	fmt.Fprintf(stdout, "# %s (%s)\n", path, state)
	return toml.NewEncoder(stdout).Encode(cfg)
}
