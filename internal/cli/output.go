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
	w    io.Writer
	json bool
	now  func() time.Time
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

// table prints rows under a header with aligned columns.
func (p *printer) table(header []string, rows [][]string) {
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
func refAt(d domain.Deployment) string {
	if d.Commit == "" {
		return d.Ref
	}
	return d.Ref + " @ " + shortCommit(d.Commit)
}

func errorText(e *domain.Error) string {
	if e == nil {
		return ""
	}
	return e.Message + " (" + string(e.Code) + ")"
}
