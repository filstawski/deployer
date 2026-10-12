package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/huh/v2"
	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
)

// Option is one choice of a select field.
type Option struct {
	Label string
	Value string
}

// Field is one question of a form.
type Field struct {
	Title       string
	Description string
	// Options makes the field a choice; without options it is a text input.
	Options []Option
	// Value holds the default on entry and the answer on return.
	Value *string
	// Suggest returns an input's placeholder, which becomes the answer when
	// the input is left empty. SuggestFrom is the value it depends on, so the
	// placeholder follows changes to another field.
	Suggest     func() string
	SuggestFrom *string
	Validate    func(string) error
	Secret      bool
}

// Prompter asks the person at the terminal.
type Prompter interface {
	// Ask shows the fields together as one form.
	Ask(fields ...Field) error
	// Confirm asks a yes or no question, defaulting to no.
	Confirm(title, description string) (bool, error)
}

// canPrompt reports whether questions can be asked: a person is at the
// terminal and the output is not meant for a program.
func (a *app) canPrompt() bool {
	return a.env.Prompter != nil && a.env.Interactive && !a.json
}

// summaryWidth aligns the labels of answer summaries.
const summaryWidth = 8

// ask runs a form. Inputs left empty take their suggestion. The form
// disappears once answered, so the answers are summarized in its place.
func (a *app) ask(fields ...Field) error {
	if err := a.env.Prompter.Ask(fields...); err != nil {
		return promptError(err)
	}
	width := summaryWidth
	for _, f := range fields {
		width = max(width, len(f.Title))
	}
	for _, f := range fields {
		if f.Options == nil && f.Suggest != nil && strings.TrimSpace(*f.Value) == "" {
			*f.Value = f.Suggest()
		}
		*f.Value = strings.TrimSpace(*f.Value)
		answer := *f.Value
		for _, o := range f.Options {
			if o.Value == answer {
				answer = o.Label
			}
		}
		if f.Secret {
			answer = strings.Repeat("•", 8)
		}
		if len(fields) > 1 || !strings.HasSuffix(f.Title, "?") {
			a.progress("%s  %s", a.errs.muted(fmt.Sprintf("%-*s", width, f.Title)), a.errs.bold(answer))
		}
	}
	return nil
}

func (a *app) confirmPrompt(title, description string) (bool, error) {
	ok, err := a.env.Prompter.Confirm(title, description)
	return ok, promptError(err)
}

func promptError(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return domain.Errorf(domain.CodeClient, "cancelled")
	}
	return err
}

// needsName fails early, before connecting, when a deployment name is
// missing and cannot be asked for.
func (a *app) needsName(args []string, command string) error {
	if len(args) == 0 && !a.canPrompt() {
		return usage("deployer %s needs a deployment name", command).WithHint("see your deployments with: deployer list")
	}
	return nil
}

// deploymentArg returns the deployment named in args. Without one, it lets
// the user pick among the deployments that keep accepts.
func (a *app) deploymentArg(ctx context.Context, c *client.Client, args []string, verb string, keep func(domain.Deployment) bool) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	list, err := c.Deployments(ctx, client.ListOptions{})
	if err != nil {
		return "", err
	}
	var options []Option
	for _, d := range list {
		if keep == nil || keep(d) {
			options = append(options, Option{Label: deploymentLabel(d), Value: d.Name})
		}
	}
	if len(options) == 0 {
		return "", domain.Errorf(domain.CodeNotFound, "there is no deployment to %s", verb).WithHint("see your deployments with: deployer list")
	}
	choice := options[0].Value
	if err := a.ask(Field{Title: "Which deployment do you want to " + verb + "?", Options: options, Value: &choice}); err != nil {
		return "", err
	}
	return choice, nil
}

func deploymentLabel(d domain.Deployment) string {
	return fmt.Sprintf("%-28s %-9s %s", d.Name, d.State, d.Ref)
}

// huhPrompter asks questions with huh forms.
type huhPrompter struct {
	in  io.Reader
	out io.Writer
}

func (h huhPrompter) form(fields ...huh.Field) *huh.Form {
	return huh.NewForm(huh.NewGroup(fields...)).
		WithInput(h.in).WithOutput(h.out).
		WithTheme(huh.ThemeFunc(huh.ThemeBase16)).
		WithShowHelp(true)
}

func (h huhPrompter) Ask(fields ...Field) error {
	var form []huh.Field
	for _, f := range fields {
		if len(f.Options) > 0 {
			options := make([]huh.Option[string], len(f.Options))
			for n, o := range f.Options {
				options[n] = huh.NewOption(o.Label, o.Value)
			}
			s := huh.NewSelect[string]().Title(f.Title).Description(f.Description).Options(options...).Value(f.Value)
			if len(options) > 8 {
				s = s.Height(10).Filtering(false)
			}
			form = append(form, s)
			continue
		}
		in := huh.NewInput().Title(f.Title).Description(f.Description).Value(f.Value)
		if f.Suggest != nil {
			binding := any(f.SuggestFrom)
			if f.SuggestFrom == nil {
				binding = f.Value
			}
			in = in.PlaceholderFunc(f.Suggest, binding)
		}
		if f.Validate != nil {
			validate, suggested := f.Validate, f.Suggest != nil
			in = in.Validate(func(v string) error {
				if strings.TrimSpace(v) == "" && suggested {
					return nil
				}
				return validate(strings.TrimSpace(v))
			})
		}
		if f.Secret {
			in = in.EchoMode(huh.EchoModePassword)
		}
		form = append(form, in)
	}
	return h.form(form...).Run()
}

func (h huhPrompter) Confirm(title, description string) (bool, error) {
	var ok bool
	err := h.form(huh.NewConfirm().Title(title).Description(description).Affirmative("Yes").Negative("No").Value(&ok)).Run()
	return ok, err
}
