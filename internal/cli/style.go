package cli

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/filstawski/deployer/internal/domain"
)

// styles renders text for a terminal. When disabled, for pipes, files, and
// JSON output, every method returns plain text, so output that scripts read
// keeps its simple, stable shape.
//
// Colors are the terminal's 16 ANSI colors, which follow the user's theme in
// light and dark mode. The colorprofile writer removes them where they are not
// supported or NO_COLOR is set.
type styles struct {
	enabled bool
	links   bool // The terminal supports OSC 8 hyperlinks.
}

var (
	colorOK     = lipgloss.Green
	colorBusy   = lipgloss.Yellow
	colorBad    = lipgloss.Red
	colorAccent = lipgloss.Cyan
	colorMuted  = lipgloss.BrightBlack
)

func (s styles) paint(text string, style lipgloss.Style) string {
	if !s.enabled || text == "" {
		return text
	}
	return style.Render(text)
}

func (s styles) fg(text string, c color.Color) string {
	return s.paint(text, lipgloss.NewStyle().Foreground(c))
}

func (s styles) ok(text string) string    { return s.fg(text, colorOK) }
func (s styles) warn(text string) string  { return s.fg(text, colorBusy) }
func (s styles) bad(text string) string   { return s.fg(text, colorBad) }
func (s styles) muted(text string) string { return s.fg(text, colorMuted) }
func (s styles) bold(text string) string  { return s.paint(text, lipgloss.NewStyle().Bold(true)) }

// url renders a link, clickable in terminals that support it.
func (s styles) url(u string) string {
	// UnderlineSpaces avoids styling each character separately.
	style := lipgloss.NewStyle().Foreground(colorAccent).Underline(true).UnderlineSpaces(true)
	if s.links {
		style = style.Hyperlink(u)
	}
	return s.paint(u, style)
}

// command renders a command the user can run.
func (s styles) command(c string) string {
	return s.paint(c, lipgloss.NewStyle().Foreground(colorAccent))
}

// stateColor is the color of a deployment state.
func stateColor(st domain.State) color.Color {
	switch st {
	case domain.StateRunning:
		return colorOK
	case domain.StateQueued, domain.StateBuilding, domain.StateStarting, domain.StateExpired:
		return colorBusy
	case domain.StateFailed:
		return colorBad
	default:
		return colorMuted
	}
}

// state renders a deployment state as a colored badge, such as "● running".
func (s styles) state(st domain.State) string {
	if !s.enabled {
		return string(st)
	}
	symbol := map[domain.State]string{
		domain.StateRunning: "●", domain.StateQueued: "◐", domain.StateBuilding: "◐", domain.StateStarting: "◐",
		domain.StateStopped: "○", domain.StateExpired: "◌", domain.StateFailed: "✗", domain.StateDestroyed: "–",
	}[st]
	return s.fg(symbol+" "+string(st), stateColor(st))
}

// panel is a bordered box for one result, such as a deployment's details.
type panel struct {
	title  string      // Shown in the top border.
	accent color.Color // Border color.
	lines  []string    // Free text at the top, such as a URL.
	fields [][2]string // Label and value rows; empty values are skipped.
	notes  []string    // Free text at the bottom, such as a hint.
}

// render draws the panel no wider than width columns.
func (p panel) render(s styles, width int) string {
	var body []string
	body = append(body, p.lines...)
	labelWidth := 0
	for _, f := range p.fields {
		if f[1] != "" {
			labelWidth = max(labelWidth, lipgloss.Width(f[0]))
		}
	}
	var rows []string
	for _, f := range p.fields {
		if f[1] != "" {
			rows = append(rows, s.muted(f[0]+strings.Repeat(" ", labelWidth-lipgloss.Width(f[0])))+"  "+f[1])
		}
	}
	if len(rows) > 0 {
		if len(body) > 0 {
			body = append(body, "")
		}
		body = append(body, rows...)
	}
	if len(p.notes) > 0 {
		body = append(body, "")
		body = append(body, p.notes...)
	}

	border := lipgloss.RoundedBorder()
	box := lipgloss.NewStyle().Border(border, false, true, true, true).BorderForeground(p.accent).Padding(0, 1)
	content := strings.Join(body, "\n")
	inner := lipgloss.Width(content) + 2
	titleWidth := lipgloss.Width(p.title) + 5
	if width > 0 {
		inner = min(max(inner, titleWidth), width-2)
		box = box.Width(inner + 2)
	} else if inner < titleWidth {
		box = box.Width(titleWidth + 2)
	}
	rendered := box.Render(content)

	// lipgloss has no titled borders, so draw the top edge with the title.
	total := lipgloss.Width(rendered)
	edge := lipgloss.NewStyle().Foreground(p.accent)
	fill := max(total-lipgloss.Width(p.title)-5, 1)
	top := edge.Render(border.TopLeft+border.Top+" ") + s.bold(p.title) + edge.Render(" "+strings.Repeat(border.Top, fill)+border.TopRight)
	return top + "\n" + rendered
}

// richTable renders rows as aligned columns under a muted header. A table
// wider than the terminal wraps within its columns rather than at the edge.
func richTable(s styles, header []string, rows [][]string, width int) string {
	t := table.New().Headers(header...).Rows(rows...).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderHeader(false).BorderRow(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := lipgloss.NewStyle().PaddingRight(2)
			if row == table.HeaderRow {
				style = style.Foreground(colorMuted).Bold(true)
			}
			return style
		})
	out := t.Render()
	if width > 0 && lipgloss.Width(out) > width {
		out = t.Width(width).Render()
	}
	return out
}
