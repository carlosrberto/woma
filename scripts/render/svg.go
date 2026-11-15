package main

import (
	"fmt"
	"html"
	"strings"
)

// Cell geometry, in SVG units: a 14 px monospace font's cell.
const (
	fontSize = 14.0
	cellW    = 8.4
	cellH    = 18.0
	pad      = 20.0
	titleH   = 36.0
	// seam is how far a background or a solid separator reaches into the
	// next cell: edges between pixels are blended, and two shapes meeting
	// there leave a line of the background between them.
	seam = 0.5
)

const fonts = `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'DejaVu Sans Mono', monospace`

// svg draws scr in a window's frame titled title.
func svg(scr screen, p palette, title string) string {
	cols := 0
	for _, row := range scr {
		cols = max(cols, len(row))
	}
	w := 2*pad + float64(cols)*cellW
	// Wide enough for the title, centred clear of the three buttons (13 px
	// monospace: about 7.8 a character).
	w = max(w, float64(len([]rune(title)))*7.8+2*(pad+56))
	h := titleH + 2*pad + float64(len(scr))*cellH - pad/2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" role="img" aria-label="%s">`+"\n",
		n(w), n(h), n(w), n(h), html.EscapeString(title))
	fmt.Fprintf(&b, `<rect width="%s" height="%s" rx="10" fill="%s"/>`+"\n", n(w), n(h), p.bg)
	fmt.Fprintf(&b, `<rect width="%s" height="%s" rx="10" fill="none" stroke="%s"/>`+"\n", n(w), n(h), mix(p.bg, p.fg, 0.15))
	for i, c := range []string{"#ff5f57", "#febc2e", "#28c840"} {
		fmt.Fprintf(&b, `<circle cx="%s" cy="%s" r="6" fill="%s"/>`+"\n", n(pad+float64(i)*20), n(titleH/2+2), c)
	}
	fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" font-family="%s" font-size="13" fill="%s">%s</text>`+"\n",
		n(w/2), n(titleH/2+6), fonts, mix(p.fg, p.bg, 0.4), html.EscapeString(title))
	fmt.Fprintf(&b, `<g transform="translate(%s %s)" font-family="%s" font-size="%s">`+"\n", n(pad), n(titleH+pad/2), fonts, n(fontSize))
	for y, row := range scr {
		drawRow(&b, row, float64(y)*cellH, p)
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

// drawRow draws a row: each run of one style's background, its shapes,
// then its text.
func drawRow(b *strings.Builder, row []cell, y float64, p palette) {
	var bgs, shapes, texts strings.Builder
	for start := 0; start < len(row); {
		end := start + 1
		for end < len(row) && row[end].st == row[start].st {
			end++
		}
		st := row[start].st
		fg, bg := st.colors(p)
		x := float64(start) * cellW
		if bg != p.bg {
			w := float64(end-start) * cellW
			if end < len(row) { // under the next run's background, if it has one
				if _, next := row[end].st.colors(p); next != p.bg {
					w += seam
				}
			}
			fmt.Fprintf(&bgs, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`+"\n", n(x), n(y), n(w), n(cellH), bg)
		}
		var text strings.Builder
		for i := start; i < end; i++ {
			c := row[i]
			if c.width == 0 {
				continue
			}
			if r := []rune(c.text)[0]; shape(&shapes, r, float64(i)*cellW, y, fg) {
				text.WriteString(strings.Repeat(" ", c.width))
				continue
			}
			text.WriteString(c.text)
		}
		if t := text.String(); strings.TrimSpace(t) != "" {
			attrs := ""
			if st.bold {
				attrs += ` font-weight="bold"`
			}
			if st.italic {
				attrs += ` font-style="italic"`
			}
			if st.underline {
				attrs += ` text-decoration="underline"`
			}
			fmt.Fprintf(&texts, `<text x="%s" y="%s" textLength="%s" lengthAdjust="spacingAndGlyphs" xml:space="preserve" fill="%s"%s>%s</text>`+"\n",
				n(x), n(y+cellH*0.74), n(float64(end-start)*cellW), fg, attrs, html.EscapeString(t))
		}
		start = end
	}
	b.WriteString(bgs.String())
	b.WriteString(shapes.String())
	b.WriteString(texts.String())
}

// arms are a box-drawing character's lines from its cell's middle: left,
// right, up, down; round for its rounded corners, heavy for thick lines.
type arms struct{ l, r, u, d, round, heavy bool }

var boxes = map[rune]arms{
	'─': {l: true, r: true}, '│': {u: true, d: true}, '━': {l: true, r: true, heavy: true}, '┃': {u: true, d: true, heavy: true},
	'┌': {r: true, d: true}, '┐': {l: true, d: true}, '└': {r: true, u: true}, '┘': {l: true, u: true},
	'├': {u: true, d: true, r: true}, '┤': {u: true, d: true, l: true}, '┬': {l: true, r: true, d: true}, '┴': {l: true, r: true, u: true},
	'┼': {l: true, r: true, u: true, d: true},
	'╭': {r: true, d: true, round: true}, '╮': {l: true, d: true, round: true}, '╰': {r: true, u: true, round: true}, '╯': {l: true, u: true, round: true},
	'═': {l: true, r: true}, '║': {u: true, d: true},
}

// shape draws c at (x, y) as shapes when it's box drawing, a block
// element or a powerline separator, and says whether it did.
func shape(b *strings.Builder, c rune, x, y float64, fg string) bool {
	cx, cy := x+cellW/2, y+cellH/2
	if a, ok := boxes[c]; ok {
		sw := 1.2
		if a.heavy {
			sw = 2.4
		}
		var d string
		if a.round {
			r := cellW / 2
			hx, sx := x, -1.0
			if a.r {
				hx, sx = x+cellW, 1
			}
			vy, sy := y, -1.0
			if a.d {
				vy, sy = y+cellH, 1
			}
			d = fmt.Sprintf("M%s %sL%s %sQ%s %s %s %sL%s %s", n(hx), n(cy), n(cx+sx*r), n(cy), n(cx), n(cy), n(cx), n(cy+sy*r), n(cx), n(vy))
		} else {
			if a.l || a.r {
				x0, x1 := cx, cx
				if a.l {
					x0 = x
				}
				if a.r {
					x1 = x + cellW
				}
				d += fmt.Sprintf("M%s %sH%s", n(x0), n(cy), n(x1))
			}
			if a.u || a.d {
				y0, y1 := cy, cy
				if a.u {
					y0 = y
				}
				if a.d {
					y1 = y + cellH
				}
				d += fmt.Sprintf("M%s %sV%s", n(cx), n(y0), n(y1))
			}
		}
		fmt.Fprintf(b, `<path d="%s" fill="none" stroke="%s" stroke-width="%s"/>`+"\n", d, fg, n(sw))
		return true
	}
	rect := func(x0, y0, w, h float64, op string) {
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"%s/>`+"\n", n(x0), n(y0), n(w), n(h), fg, op)
	}
	switch {
	case c == '▀':
		rect(x, y, cellW, cellH/2, "")
	case c >= '▁' && c <= '█': // lower eighths
		h := cellH * float64(c-'▁'+1) / 8
		rect(x, y+cellH-h, cellW, h, "")
	case c >= '▉' && c <= '▏': // left eighths
		rect(x, y, cellW*float64('▏'-c+1)/8, cellH, "")
	case c == '▐':
		rect(x+cellW/2, y, cellW/2, cellH, "")
	case c >= '░' && c <= '▓':
		rect(x, y, cellW, cellH, fmt.Sprintf(` opacity="%s"`, n(float64(c-'░'+1)/4)))
	case c == '▔':
		rect(x, y, cellW, cellH/8, "")
	case c == '▕':
		rect(x+cellW*7/8, y, cellW/8, cellH, "")
	case c >= 0xE0B0 && c <= 0xE0B7:
		r, t := x+cellW, y+cellH
		// A solid one's flat side reaches into the cell it continues.
		xs, rs := x-seam, r+seam
		var d, fill, stroke string
		switch c {
		case 0xE0B0: // a solid arrow, pointing right
			d = fmt.Sprintf("M%s %sL%s %sL%s %sZ", n(xs), n(y), n(r), n(cy), n(xs), n(t))
		case 0xE0B2:
			d = fmt.Sprintf("M%s %sL%s %sL%s %sZ", n(rs), n(y), n(x), n(cy), n(rs), n(t))
		case 0xE0B4: // a solid half circle, bulging right
			d = fmt.Sprintf("M%s %sA%s %s 0 0 1 %s %sZ", n(xs), n(y), n(cellW), n(cellH/2), n(xs), n(t))
		case 0xE0B6:
			d = fmt.Sprintf("M%s %sA%s %s 0 0 0 %s %sZ", n(rs), n(y), n(cellW), n(cellH/2), n(rs), n(t))
		case 0xE0B1:
			d = fmt.Sprintf("M%s %sL%s %sL%s %s", n(x), n(y), n(r), n(cy), n(x), n(t))
		case 0xE0B3:
			d = fmt.Sprintf("M%s %sL%s %sL%s %s", n(r), n(y), n(x), n(cy), n(r), n(t))
		case 0xE0B5:
			d = fmt.Sprintf("M%s %sA%s %s 0 0 1 %s %s", n(x), n(y), n(cellW), n(cellH/2), n(x), n(t))
		case 0xE0B7:
			d = fmt.Sprintf("M%s %sA%s %s 0 0 0 %s %s", n(r), n(y), n(cellW), n(cellH/2), n(r), n(t))
		}
		if c%2 == 0 {
			fill, stroke = fg, "none"
		} else {
			fill, stroke = "none", fg
		}
		fmt.Fprintf(b, `<path d="%s" fill="%s" stroke="%s"/>`+"\n", d, fill, stroke)
	default:
		return false
	}
	return true
}

// n prints a coordinate short: at most two decimals, no trailing zeros.
func n(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}
