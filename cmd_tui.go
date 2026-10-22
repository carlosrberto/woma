package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"woma/internal/config"
	"woma/internal/tui"
)

func runBrowse(args []string, cfg config.Config, stdout, stderr io.Writer) error {
	fs := newFlagSet("woma", "woma [path...]", stderr)
	paths, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	m, err := runTUI(paths, cfg, tui.Options{}, os.Stdout)
	if err != nil {
		return err
	}
	if n, freed := m.Summary(); n > 0 {
		fmt.Fprintf(stdout, "Removed %d worktree(s), freed %s.\n", n, tui.HumanBytes(freed))
	}
	return nil
}

// runPick shows the TUI on stderr and prints the chosen path on stdout, so
// it works inside $(...).
func runPick(args []string, cfg config.Config, stdout, stderr io.Writer) error {
	fs := newFlagSet("pick", "woma pick [path...] [--multi]", stderr)
	multi := fs.Bool("multi", false, "select several worktrees with space; prints one path per line")
	paths, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	tui.UseOutput(os.Stderr)
	m, err := runTUI(paths, cfg, tui.Options{Pick: true, Multi: *multi}, os.Stderr)
	if err != nil {
		return err
	}
	if len(m.Picked()) == 0 {
		return errSilent
	}
	for _, p := range m.Picked() {
		fmt.Fprintln(stdout, p)
	}
	return nil
}

// runTUI runs the browser; opts gets the roots and config filled in.
func runTUI(paths []string, cfg config.Config, opts tui.Options, out io.Writer) (*tui.Model, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git not found in PATH")
	}
	roots, err := resolveRoots(paths, cfg)
	if err != nil {
		return nil, err
	}
	opts.Roots, opts.Config = roots, cfg
	m := tui.New(opts)
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(out)).Run(); err != nil {
		return nil, err
	}
	return m, m.Err()
}
