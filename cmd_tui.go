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
	m, err := runTUI(paths, cfg, false, os.Stdout)
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
	fs := newFlagSet("pick", "woma pick [path...]", stderr)
	paths, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	tui.UseOutput(os.Stderr)
	m, err := runTUI(paths, cfg, true, os.Stderr)
	if err != nil {
		return err
	}
	if m.Picked() == "" {
		return errSilent
	}
	fmt.Fprintln(stdout, m.Picked())
	return nil
}

func runTUI(paths []string, cfg config.Config, pick bool, out io.Writer) (*tui.Model, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git not found in PATH")
	}
	roots, err := resolveRoots(paths, cfg)
	if err != nil {
		return nil, err
	}
	m := tui.New(tui.Options{Roots: roots, Config: cfg, Pick: pick})
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(out)).Run(); err != nil {
		return nil, err
	}
	return m, m.Err()
}
