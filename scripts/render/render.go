package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// style is a cell's look as the SGR codes set it; "" colours are the
// terminal's own.
type style struct {
	fg, bg                                string
	bold, dim, italic, underline, reverse bool
}

// cell is one column of the screen: its text (a rune and what combines
// with it; "" for the second half of a wide one) and its look.
type cell struct {
	text  string
	width int
	st    style
}

// screen is a capture: its rows of cells.
type screen [][]cell

// palette is the terminal's 16 colours and its own foreground and
// background: tokyonight's, the screenshots' terminal.
type palette struct {
	fg, bg string
	ansi   [16]string
}

var tokyonight = palette{
	fg: "#c0caf5", bg: "#1a1b26",
	ansi: [16]string{"#15161e", "#f7768e", "#9ece6a", "#e0af68", "#7aa2f7", "#bb9af7", "#7dcfff", "#a9b1d6",
		"#414868", "#f7768e", "#9ece6a", "#e0af68", "#7aa2f7", "#bb9af7", "#7dcfff", "#c0caf5"},
}

// parse reads text with SGR escapes (tmux capture-pane -e) into cells;
// other escapes are dropped. A line starts in the style the one before
// ended in: capture-pane writes only what changes, across lines too.
func parse(r io.Reader, p palette) screen {
	var scr screen
	var st style
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var row []cell
		line := []rune(sc.Text())
		for i := 0; i < len(line); i++ {
			c := line[i]
			if c == 0x1b {
				j := i + 1
				if j < len(line) && line[j] == '[' {
					j++
					start := j
					for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
						j++
					}
					if j < len(line) && line[j] == 'm' {
						st = sgr(st, string(line[start:j]), p)
					}
				}
				i = j
				continue
			}
			w := runewidth.RuneWidth(c)
			if w == 0 && len(row) > 0 && c >= 0x20 { // combines with the cell before
				for k := len(row) - 1; k >= 0; k-- {
					if row[k].width > 0 {
						row[k].text += string(c)
						break
					}
				}
				continue
			}
			if c < 0x20 {
				continue
			}
			row = append(row, cell{text: string(c), width: w, st: st})
			if w == 2 {
				row = append(row, cell{width: 0, st: st})
			}
		}
		scr = append(scr, row)
	}
	return scr
}

// sgr applies one SGR sequence's parameters ("1;38;5;141") to st.
func sgr(st style, params string, p palette) style {
	if params == "" {
		return style{}
	}
	ps := strings.Split(params, ";")
	num := func(s string) int { n, _ := strconv.Atoi(strings.SplitN(s, ":", 2)[0]); return n }
	for i := 0; i < len(ps); i++ {
		n := num(ps[i])
		switch {
		case n == 0:
			st = style{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.dim = true
		case n == 3:
			st.italic = true
		case n == 4:
			st.underline = !strings.HasPrefix(ps[i], "4:0")
		case n == 7:
			st.reverse = true
		case n == 22:
			st.bold, st.dim = false, false
		case n == 23:
			st.italic = false
		case n == 24:
			st.underline = false
		case n == 27:
			st.reverse = false
		case n >= 30 && n <= 37:
			st.fg = p.ansi[n-30]
		case n >= 90 && n <= 97:
			st.fg = p.ansi[n-90+8]
		case n >= 40 && n <= 47:
			st.bg = p.ansi[n-40]
		case n >= 100 && n <= 107:
			st.bg = p.ansi[n-100+8]
		case n == 39:
			st.fg = ""
		case n == 49:
			st.bg = ""
		case n == 38 || n == 48 || n == 58:
			var col string
			if strings.Contains(ps[i], ":") { // 38:5:n or 38:2:[cs]:r:g:b
				col = extColor(strings.Split(ps[i], ":")[1:], p)
			} else {
				var used int
				col, used = extColorArgs(ps[i+1:], p)
				i += used
			}
			switch n {
			case 38:
				st.fg = col
			case 48:
				st.bg = col
			}
		}
	}
	return st
}

// extColorArgs reads a 38/48's colour from the parameters after it: "5;n"
// or "2;r;g;b"; it says how many it used.
func extColorArgs(ps []string, p palette) (string, int) {
	if len(ps) >= 2 && ps[0] == "5" {
		return extColor(ps[:2], p), 2
	}
	if len(ps) >= 4 && ps[0] == "2" {
		return extColor(ps[:4], p), 4
	}
	return "", len(ps)
}

// extColor is a colour from its parts: ["5", n] or ["2", (cs,) r, g, b].
func extColor(parts []string, p palette) string {
	if len(parts) == 0 {
		return ""
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	switch parts[0] {
	case "5":
		if len(parts) > 1 {
			return color256(n(parts[1]), p)
		}
	case "2":
		rgb := parts[1:]
		if len(rgb) == 4 { // a colour space first
			rgb = rgb[1:]
		}
		if len(rgb) == 3 {
			return fmt.Sprintf("#%02x%02x%02x", n(rgb[0]), n(rgb[1]), n(rgb[2]))
		}
	}
	return ""
}

// color256 is xterm's 256-colour palette, the first 16 the terminal's.
func color256(n int, p palette) string {
	switch {
	case n < 16:
		return p.ansi[max(n, 0)]
	case n < 232:
		n -= 16
		lv := []int{0, 95, 135, 175, 215, 255}
		return fmt.Sprintf("#%02x%02x%02x", lv[n/36], lv[n/6%6], lv[n%6])
	case n < 256:
		g := 8 + 10*(n-232)
		return fmt.Sprintf("#%02x%02x%02x", g, g, g)
	}
	return ""
}

// colors is a cell's foreground and background as drawn: the terminal's
// for "", swapped by reverse, a dim foreground halfway to its background.
func (st style) colors(p palette) (fg, bg string) {
	fg, bg = st.fg, st.bg
	if fg == "" {
		fg = p.fg
	}
	if bg == "" {
		bg = p.bg
	}
	if st.reverse {
		fg, bg = bg, fg
	}
	if st.dim {
		fg = mix(fg, bg, 0.45)
	}
	return fg, bg
}

// mix is a blended toward b by t (0: a, 1: b).
func mix(a, b string, t float64) string {
	ra, ga, ba := rgb(a)
	rb, gb, bb := rgb(b)
	f := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", f(ra, rb), f(ga, gb), f(ba, bb))
}

func rgb(h string) (r, g, b int) {
	fmt.Sscanf(strings.TrimPrefix(h, "#"), "%02x%02x%02x", &r, &g, &b)
	return
}

// trim is scr without its blank rows at the bottom: a screen drawn in a
// pane taller than it.
func trim(scr screen, p palette) screen {
	for len(scr) > 0 && blank(scr[len(scr)-1], p) {
		scr = scr[:len(scr)-1]
	}
	return scr
}

// blank reports whether a row draws nothing: spaces on the terminal's
// background.
func blank(row []cell, p palette) bool {
	for _, c := range row {
		if _, bg := c.st.colors(p); strings.TrimSpace(c.text) != "" || bg != p.bg {
			return false
		}
	}
	return true
}
