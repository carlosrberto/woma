package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"

	"woma/internal/core"
	"woma/internal/git"
)

type styleSet struct {
	renderer                               *lipgloss.Renderer
	title, repo, dim, bold, pointer, check lipgloss.Style
	size, warn, err, ok, spinner, key      lipgloss.Style
}

var styles = newStyles(lipgloss.DefaultRenderer())

func newStyles(r *lipgloss.Renderer) styleSet {
	c := func(color string) lipgloss.Style { return r.NewStyle().Foreground(lipgloss.Color(color)) }
	return styleSet{
		renderer: r,
		title:    c("212").Bold(true),
		repo:     c("39").Bold(true),
		dim:      c("244"),
		bold:     r.NewStyle().Bold(true),
		pointer:  c("212").Bold(true),
		check:    c("42").Bold(true),
		size:     c("214"),
		warn:     c("214"),
		err:      c("196"),
		ok:       c("42"),
		spinner:  c("212"),
		key:      c("212"),
	}
}

// UseOutput makes the TUI detect colors from w. Pick mode draws on stderr
// so stdout only carries the chosen path.
func UseOutput(w io.Writer) {
	styles = newStyles(lipgloss.NewRenderer(w))
}

func newBar() progress.Model {
	return progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage(),
		progress.WithWidth(40), progress.WithColorProfile(styles.renderer.ColorProfile()))
}

func labelStyle(k core.LabelKind) lipgloss.Style {
	switch k {
	case core.LabelGood:
		return styles.ok
	case core.LabelWarn:
		return styles.warn
	case core.LabelDanger:
		return styles.err
	}
	return styles.dim
}

// displayPath shows a worktree relative to its repository or a scan root
// when it lives inside them.
func (m *Model) displayPath(r *git.Repo, path string) string {
	for _, base := range append([]string{r.Path}, m.opts.Roots...) {
		if rel, ok := within(base, path); ok {
			return rel
		}
	}
	return tildify(path)
}

func within(base, path string) (string, bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func tildify(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// HumanBytes formats a byte count using binary units.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Age formats how long ago t was compactly: "5m", "3h", "2d", "3w", "4mo", "1y".
func Age(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	day := 24 * time.Hour
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < day:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d < 14*day:
		return fmt.Sprintf("%dd", int(d/day))
	case d < 60*day:
		return fmt.Sprintf("%dw", int(d/(7*day)))
	case d < 365*day:
		return fmt.Sprintf("%dmo", int(d/(30*day)))
	}
	return fmt.Sprintf("%dy", int(d/(365*day)))
}

// ago is Age as a phrase: "3d ago", "now" or "unknown".
func ago(t, now time.Time) string {
	switch a := Age(t, now); a {
	case "now":
		return a
	case "—":
		return "unknown"
	default:
		return a + " ago"
	}
}

func plural(n int, one string, many ...string) string {
	if n == 1 {
		return one
	}
	if len(many) > 0 {
		return many[0]
	}
	return one + "s"
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// truncLeft keeps the end of s, which is the informative part of a path.
func truncLeft(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w < 1 {
		return s
	}
	return "…" + string(r[len(r)-w+1:])
}

func truncRight(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w < 1 {
		return s
	}
	return string(r[:w-1]) + "…"
}

func branchLabel(wt git.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	if len(wt.Head) >= 7 {
		return "(detached " + wt.Head[:7] + ")"
	}
	return "(detached)"
}
