// Command render draws a terminal capture (tmux capture-pane -e) as an SVG
// in a window's frame, for woma's screenshots (scripts/screenshots.sh).
//
//	render [-title t] [-trim] <capture.ansi> <out.svg>
//
// It first rewrites the capture in one form (canon), so two captures of
// one screen make the same SVG. -trim leaves out the blank rows at the
// bottom.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	title := flag.String("title", "woma", "the window's title")
	cut := flag.Bool("trim", false, "leave out the blank rows at the bottom")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: render [-title t] [-trim] <capture.ansi> <out.svg>")
		os.Exit(2)
	}
	text, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "render:", err)
		os.Exit(1)
	}
	scr := parse(strings.NewReader(canon(string(text))), tokyonight)
	if *cut {
		scr = trim(scr, tokyonight)
	}
	if err := os.WriteFile(flag.Arg(1), []byte(svg(scr, tokyonight, *title)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "render:", err)
		os.Exit(1)
	}
}
