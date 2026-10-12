package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/filstawski/deployer/internal/domain"
)

// fakePrompter answers questions by title. Unanswered fields keep their
// defaults, as when the user just presses Enter.
type fakePrompter struct {
	t       *testing.T
	answers map[string]string
	confirm bool
	asked   []string
	options map[string][]Option
}

func (f *fakePrompter) Ask(fields ...Field) error {
	for _, field := range fields {
		f.asked = append(f.asked, field.Title)
		if f.options == nil {
			f.options = map[string][]Option{}
		}
		f.options[field.Title] = field.Options
		answer, ok := f.answers[field.Title]
		if !ok {
			continue
		}
		if field.Options != nil && !slices.ContainsFunc(field.Options, func(o Option) bool { return o.Value == answer }) {
			f.t.Fatalf("%q is not an option of %q: %v", answer, field.Title, field.Options)
		}
		if field.Validate != nil && answer != "" {
			if err := field.Validate(answer); err != nil {
				return err
			}
		}
		*field.Value = answer
	}
	return nil
}

func (f *fakePrompter) Confirm(title, description string) (bool, error) {
	f.asked = append(f.asked, title)
	return f.confirm, nil
}

func (h *harness) interactive(p *fakePrompter, args ...string) result {
	h.t.Helper()
	return h.runEnv(func(e *Env) { e.Interactive, e.Prompter = true, p }, args...)
}

func TestDeployForm(t *testing.T) {
	h := newHarness(t)
	h.login()
	git(t, h.dir, "init", "--quiet", "-b", "main")
	git(t, h.dir, "remote", "add", "origin", "https://github.com/acme/shop.git")
	git(t, h.dir, "commit", "--quiet", "--allow-empty", "-m", "init")
	for _, b := range []string{"main", "design/a"} {
		git(t, h.dir, "update-ref", "refs/remotes/origin/"+b, "HEAD")
	}

	p := &fakePrompter{t: t, answers: map[string]string{"Branch": "design/a", "Lifetime": "259200"}}
	r := h.interactive(p, "deploy")
	if r.code != 0 {
		t.Fatalf("deploy: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if !slices.Equal(p.asked, []string{"Branch", "Name", "Lifetime"}) {
		t.Fatalf("asked %v", p.asked)
	}
	branches := p.options["Branch"]
	if len(branches) != 2 || branches[0].Value != "main" || !strings.Contains(branches[0].Label, "current") {
		t.Fatalf("branch options: %v", branches)
	}
	d, err := h.server.Controller.Deployment(t.Context(), "shop-design-a")
	if err != nil || d.Ref != "design/a" || d.TTLSeconds != 259200 {
		t.Fatalf("deployed: %+v %v", d, err)
	}

	// The suggested name avoids live names; flags answer questions upfront.
	p = &fakePrompter{t: t, answers: map[string]string{"Branch": "design/a"}}
	h.interactive(p, "deploy", "--ttl", "1h")
	if _, err := h.server.Controller.Deployment(t.Context(), "shop-design-a-2"); err != nil || slices.Contains(p.asked, "Lifetime") {
		t.Fatalf("second suggestion: %v, asked %v", err, p.asked)
	}
	p = &fakePrompter{t: t}
	if r := h.interactive(p, "deploy", "--ref", "main", "--name", "flagged", "--ttl", "1h"); r.code != 0 || len(p.asked) != 0 {
		t.Fatalf("flags should skip the form: %d asked %v %s", r.code, p.asked, r.stderr)
	}
	p = &fakePrompter{t: t}
	if r := h.interactive(p, "deploy", "-y"); r.code != 0 || len(p.asked) != 0 {
		t.Fatalf("-y should skip the form: %d asked %v", r.code, p.asked)
	}
	// A taken name is rejected by validation.
	p = &fakePrompter{t: t, answers: map[string]string{"Name": "flagged"}}
	if r := h.interactive(p, "deploy"); r.code == 0 || !strings.Contains(r.stderr, "already named flagged") {
		t.Fatalf("taken name: %d %s", r.code, r.stderr)
	}
}

func TestPickersAndConfirmation(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.ok("project", "add", "shop", "--repo", "https://github.com/acme/shop")
	h.ok("deploy", "shop", "--ref", "main", "--name", "alpha")
	h.ok("deploy", "shop", "--ref", "main", "--name", "beta")
	h.ok("stop", "beta")

	// Only running deployments are offered for stop.
	p := &fakePrompter{t: t, answers: map[string]string{}}
	if r := h.interactive(p, "stop"); r.code != 0 {
		t.Fatalf("stop picker: %d %s", r.code, r.stderr)
	}
	if opts := p.options["Which deployment do you want to stop?"]; len(opts) != 1 || opts[0].Value != "alpha" {
		t.Fatalf("stop options: %v", opts)
	}

	// Declining the confirmation keeps the deployment.
	p = &fakePrompter{t: t, answers: map[string]string{"Which deployment do you want to destroy?": "beta"}}
	if r := h.interactive(p, "destroy"); r.code != ExitError || !strings.Contains(r.stderr, "not destroyed") {
		t.Fatalf("declined destroy: %d %s", r.code, r.stderr)
	}
	if d, _ := h.server.Controller.Deployment(t.Context(), "beta"); d.State == domain.StateDestroyed {
		t.Fatal("destroyed despite declining")
	}
	p = &fakePrompter{t: t, confirm: true}
	if r := h.interactive(p, "destroy", "beta"); r.code != 0 || !slices.Contains(p.asked, "Destroy beta?") {
		t.Fatalf("confirmed destroy: %d asked %v %s", r.code, p.asked, r.stderr)
	}

	p = &fakePrompter{t: t, answers: map[string]string{"Extend alpha by how long?": "14400"}}
	if r := h.interactive(p, "extend", "alpha"); r.code != 0 || !strings.Contains(r.stdout, "now expires") {
		t.Fatalf("extend prompt: %d %s %s", r.code, r.stdout, r.stderr)
	}
}

func TestRichOutput(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.ok("project", "add", "shop", "--repo", "https://github.com/acme/shop")
	h.ok("deploy", "shop", "--ref", "design/a", "--name", "design-a")
	r := h.runEnv(func(e *Env) { e.Terminal, e.Width = true, 60 }, "inspect", "design-a")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "╭─ design-a  ● running") {
		t.Fatalf("panel:\n%s", r.stdout)
	}
	assertBox(t, r.stdout)
	r = h.runEnv(func(e *Env) { e.Terminal = true }, "list")
	if !strings.Contains(r.stdout, "● running") || strings.Contains(r.stdout, "╭") {
		t.Fatalf("list:\n%s", r.stdout)
	}
	// JSON stays plain even at a terminal.
	r = h.runEnv(func(e *Env) { e.Terminal = true }, "inspect", "design-a", "--json")
	if strings.Contains(r.stdout, "╭") || !strings.HasPrefix(r.stdout, "{") {
		t.Fatalf("json:\n%s", r.stdout)
	}
}

func TestPanelLayout(t *testing.T) {
	st := styles{enabled: true}
	p := panel{title: "a title", accent: colorOK, lines: []string{st.url("https://example.com")},
		fields: [][2]string{{"Short", "value"}, {"Empty", ""}, {"Long label", strings.Repeat("word ", 30)}},
		notes:  []string{"a note"}}
	for _, width := range []int{0, 40, 80} {
		out := p.render(st, width)
		assertBox(t, out)
		if width > 0 && lipgloss.Width(out) > width {
			t.Errorf("width %d: rendered %d columns", width, lipgloss.Width(out))
		}
		if strings.Contains(out, "Empty") {
			t.Error("empty field shown")
		}
	}
	if got := suggestName("shop", strings.Repeat("x", 80), map[string]bool{}); len(got) > 63 {
		t.Fatalf("long suggestion: %d", len(got))
	}
	taken := map[string]bool{"shop-main": true, "shop-main-2": true}
	if got := suggestName("shop", "main", taken); got != "shop-main-3" {
		t.Fatalf("suggestion: %s", got)
	}
	opts, def := lifetimeOptions(domain.DeploymentsStatus{DefaultTTL: 7200, MaxTTL: 86400})
	labels := fmt.Sprint(opts)
	if def != "7200" || !strings.Contains(labels, "{1 hour 3600} {2 hours (default) 7200} {4 hours 14400} {1 day (maximum) 86400}") {
		t.Fatalf("lifetime options: %v %s", opts, def)
	}
	// Longer lifetimes are offered when the server allows them.
	opts, _ = lifetimeOptions(domain.DeploymentsStatus{DefaultTTL: 86400, MaxTTL: 30 * 86400})
	if labels := fmt.Sprint(opts); !strings.Contains(labels, "1 day (default)") || !strings.Contains(labels, "2 weeks") || !strings.Contains(labels, "30 days (maximum)") {
		t.Fatalf("30-day options: %v", opts)
	}
	if got := durationLabel(45 * 24 * time.Hour); got != "45 days" {
		t.Fatal(got)
	}
}

// assertBox checks that every line of a panel has the same width.
func assertBox(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(ansi.Strip(out), "\n"), "\n")
	for _, l := range lines {
		if lipgloss.Width(l) != lipgloss.Width(lines[0]) {
			t.Fatalf("ragged panel:\n%s", out)
		}
	}
	if !strings.HasPrefix(lines[0], "╭") || !strings.HasPrefix(lines[len(lines)-1], "╰") {
		t.Fatalf("not a box:\n%s", out)
	}
}

func TestCompletion(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.ok("project", "add", "shop", "--repo", "https://github.com/acme/shop")
	h.ok("deploy", "shop", "--ref", "main", "--name", "alpha")
	h.ok("deploy", "shop", "--ref", "main", "--name", "beta")
	h.ok("stop", "beta")
	r := h.ok("__complete", "stop", "")
	if !strings.Contains(r.stdout, "alpha\trunning · main") || strings.Contains(r.stdout, "beta") {
		t.Fatalf("stop completions:\n%s", r.stdout)
	}
	if r := h.ok("__complete", "deploy", ""); !strings.Contains(r.stdout, "shop\thttps://github.com/acme/shop.git") {
		t.Fatalf("project completions:\n%s", r.stdout)
	}
	if r := h.ok("__complete", "token", "create", "x", "--role", ""); !strings.Contains(r.stdout, "deployer") {
		t.Fatalf("role completions:\n%s", r.stdout)
	}
	if r := h.ok("completion", "zsh"); !strings.Contains(r.stdout, "#compdef deployer") {
		t.Fatal("no zsh completion script")
	}
}
