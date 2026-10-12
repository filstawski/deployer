package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

// printer writes command results to stdout, either for people or as JSON.
type printer struct {
	w     io.Writer
	json  bool
	now   func() time.Time
	st    styles
	width int
}

// result prints v as JSON in JSON mode, and otherwise calls human.
func (p *printer) result(v any, human func()) error {
	if p.json {
		return printJSON(p.w, v)
	}
	human()
	return nil
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table prints rows under a header with aligned columns. Cells may contain
// styled text.
func (p *printer) table(header []string, rows [][]string) {
	if p.st.enabled {
		fmt.Fprintln(p.w, richTable(p.st, header, rows, p.width))
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// fields prints label/value pairs, skipping empty values.
func (p *printer) fields(pairs [][2]string) {
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	for _, kv := range pairs {
		if kv[1] != "" {
			fmt.Fprintf(tw, "%s\t%s\n", kv[0], kv[1])
		}
	}
	tw.Flush()
}

func (p *printer) line(format string, args ...any) { fmt.Fprintf(p.w, format+"\n", args...) }

// success prints a line announcing that something worked.
func (p *printer) success(format string, args ...any) {
	fmt.Fprintln(p.w, p.st.ok("✓")+" "+fmt.Sprintf(format, args...))
}

// details prints one result: as a panel on a terminal, and otherwise as its
// lines and label/value fields, without the title.
func (p *printer) details(pn panel) {
	if p.st.enabled {
		fmt.Fprintln(p.w, pn.render(p.st, p.width))
		return
	}
	for _, l := range pn.lines {
		p.line("%s", l)
	}
	p.fields(pn.fields)
	for _, n := range pn.notes {
		p.line("\n%s", n)
	}
}

// relative renders t relative to now: "in 3h", "5m ago".
func relative(t, now time.Time) string {
	d := t.Sub(now)
	switch {
	case d > -30*time.Second && d < 30*time.Second:
		return "now"
	case d > 0:
		return "in " + domain.FormatDuration(d)
	default:
		return domain.FormatDuration(-d) + " ago"
	}
}

// when renders an optional time as "2026-10-12 14:05 (in 3h)".
func (p *printer) when(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04") + " (" + relative(*t, p.now()) + ")"
}

// shortWhen renders an optional time compactly for tables.
func (p *printer) shortWhen(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return relative(*t, p.now())
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// refAt renders "design/a @ 7d63b83b6f7e".
func refAt(d domain.Deployment) string { return styledRef(styles{}, d) }

// styledRef renders a deployment's ref and commit, such as "main @ 7d63b83b6f7e".
// A ref that is a commit is shortened instead.
func styledRef(st styles, d domain.Deployment) string {
	switch {
	case domain.IsCommitSHA(d.Ref):
		return shortCommit(d.Ref)
	case d.Commit == "":
		return d.Ref
	}
	return d.Ref + st.muted(" @ "+shortCommit(d.Commit))
}

func errorText(e *domain.Error) string {
	if e == nil {
		return ""
	}
	return e.Message + " (" + string(e.Code) + ")"
}
