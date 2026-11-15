package main

import (
	"strings"
	"testing"
)

// The cells a capture's escapes make: colours of each kind, attributes,
// a reset, a wide character's two cells, a style kept from line to line.
func TestParse(t *testing.T) {
	in := "\x1b[1;38;5;141mab\x1b[0m \x1b[38;2;10;20;30;48;5;196mc\x1b[7md\x1b[39;49;27m界e\x1b[38:2::1:2:3mf\n\x1b[94;2mg"
	scr := parse(strings.NewReader(in), tokyonight)
	if len(scr) != 2 || len(scr[0]) != 9 {
		t.Fatalf("%d rows, %d cells: %+v", len(scr), len(scr[0]), scr)
	}
	for i, want := range []style{
		{fg: "#af87ff", bold: true}, {fg: "#af87ff", bold: true}, {},
		{fg: "#0a141e", bg: "#ff0000"}, {fg: "#0a141e", bg: "#ff0000", reverse: true},
		{}, {}, {}, {fg: "#010203"},
	} {
		if scr[0][i].st != want {
			t.Errorf("cell %d %q: %+v, want %+v", i, scr[0][i].text, scr[0][i].st, want)
		}
	}
	if scr[0][5].text != "界" || scr[0][5].width != 2 || scr[0][6].width != 0 {
		t.Errorf("wide: %+v %+v", scr[0][5], scr[0][6])
	}
	if st := scr[1][0].st; st.fg != tokyonight.ansi[12] || !st.dim || st.bg != "" {
		t.Errorf("bright and dim: %+v", st)
	}
	if fg, bg := scr[0][4].st.colors(tokyonight); fg != "#ff0000" || bg != "#0a141e" {
		t.Errorf("reverse: %s on %s", fg, bg)
	}
	carried := parse(strings.NewReader("\x1b[48;5;1ma\nb"), tokyonight)
	if st := carried[1][0].st; st.bg != tokyonight.ansi[1] {
		t.Errorf("a line's style from the one before: %+v", st)
	}
}

// The SVG: a run's text in its cells, its background, box drawing and a
// powerline separator as shapes, not glyphs; text escaped. A solid
// separator reaches into the cell before it, a background under the next
// one's, so no seam shows between them.
func TestSVG(t *testing.T) {
	scr := parse(strings.NewReader("\x1b[44m a<b \x1b[34;49m\x1b[0m\n╭─┤"), tokyonight)
	out := svg(scr, tokyonight, "woma")
	for _, want := range []string{
		`<text x="0" y="13.32" textLength="42" lengthAdjust="spacingAndGlyphs" xml:space="preserve" fill="#c0caf5"> a&lt;b </text>`,
		`<rect x="0" y="0" width="42" height="18" fill="#7aa2f7"/>`,
		`<path d="M41.5 0L50.4 9L41.5 18Z" fill="#7aa2f7" stroke="none"/>`,
		`<path d="M8.4 27L8.4 27Q4.2 27 4.2 31.2L4.2 36"`,
		`<path d="M8.4 27H16.8"`,
		`<path d="M16.8 27H21M21 18V36"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "") || strings.Contains(out, "─") {
		t.Errorf("a shape drawn as text:\n%s", out)
	}
}

// The blank rows below what's drawn go; a blank row between stays.
func TestTrim(t *testing.T) {
	scr := trim(parse(strings.NewReader("a\n\nb\n  \n\x1b[41m \x1b[0m\n\n"), tokyonight), tokyonight)
	if len(scr) != 5 {
		t.Errorf("%d rows: %+v", len(scr), scr)
	}
}

// Two captures of one screen that differ only in where tmux put the
// colour changes on blank cells come out the same; what shows on a
// blank (a background, reverse, an underline) stays; a 16 colour stays
// one; canon of canon changes nothing.
func TestCanon(t *testing.T) {
	a := "\x1b[38;2;1;2;3mab\x1b[39m   \x1b[38;2;4;5;6mc\n\x1b[31m界\x1b[0m"
	b := "\x1b[38;2;1;2;3mab   \x1b[38;2;4;5;6mc\n\x1b[31m界\x1b[0m"
	if canon(a) != canon(b) {
		t.Errorf("not the same:\n%q\n%q", canon(a), canon(b))
	}
	if c := canon(canon(a)); c != canon(a) {
		t.Errorf("canon of canon:\n%q\n%q", c, canon(a))
	}
	if !strings.Contains(canon(a), "\x1b[0;31m界") {
		t.Errorf("a 16 colour or a wide rune changed: %q", canon(a))
	}
	shown := "x\x1b[48;2;9;9;9m \x1b[0;7m \x1b[0;4m \x1b[0m"
	scr := parse(strings.NewReader(canon(shown)), tokyonight)
	if st := scr[0][1].st; st.bg != "#090909" {
		t.Errorf("a blank's background: %+v", st)
	}
	if !scr[0][2].st.reverse || !scr[0][3].st.underline {
		t.Errorf("a blank's reverse or underline: %+v %+v", scr[0][2].st, scr[0][3].st)
	}
}
