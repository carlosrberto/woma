package main

import (
	"fmt"
	"strconv"
	"strings"
)

// raw is a palette that keeps the 16 colours as their indexes ("@0" to
// "@15"), so canon writes them back as they came.
var raw = func() palette {
	var p palette
	for i := range p.ansi {
		p.ansi[i] = "@" + strconv.Itoa(i)
	}
	return p
}()

// canon is a capture written one way only: two captures of one screen
// that differ in where tmux put a colour change come out the same. A
// blank cell takes the look of the cell before it, but for what shows on
// a blank - its background, reverse, underline - and each change of look
// restates it whole.
func canon(text string) string {
	scr := parse(strings.NewReader(text), raw)
	var b strings.Builder
	var prev, cur style
	for _, row := range scr {
		for _, c := range row {
			if c.width == 0 {
				continue // a wide rune's second half
			}
			st := c.st
			if c.text == " " && !st.reverse && !st.underline {
				st.fg, st.bold, st.dim, st.italic = prev.fg, prev.bold, prev.dim, prev.italic
			}
			if st != cur {
				b.WriteString(sgrOf(st))
				cur = st
			}
			b.WriteString(c.text)
			prev = st
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// sgrOf is the SGR sequence that sets a style from nothing.
func sgrOf(st style) string {
	codes := []string{"0"}
	for _, a := range []struct {
		on   bool
		code string
	}{{st.bold, "1"}, {st.dim, "2"}, {st.italic, "3"}, {st.underline, "4"}, {st.reverse, "7"}} {
		if a.on {
			codes = append(codes, a.code)
		}
	}
	if st.fg != "" {
		codes = append(codes, colorCode(st.fg, 30, 38))
	}
	if st.bg != "" {
		codes = append(codes, colorCode(st.bg, 40, 48))
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// colorCode is a colour's SGR parameters: one of the 16 by its index
// (base, or base+60 for the bright ones), any other as 24-bit.
func colorCode(col string, base, ext int) string {
	if n, err := strconv.Atoi(strings.TrimPrefix(col, "@")); err == nil && strings.HasPrefix(col, "@") {
		if n < 8 {
			return strconv.Itoa(base + n)
		}
		return strconv.Itoa(base + 60 + n - 8)
	}
	var r, g, b int
	fmt.Sscanf(col, "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("%d;2;%d;%d;%d", ext, r, g, b)
}
