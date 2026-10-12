package cli

import (
	"bytes"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/filstawski/deployer/skills"
	"github.com/spf13/cobra"
)

func TestSkillTopics(t *testing.T) {
	h := &harness{t: t, vars: map[string]string{}, config: t.TempDir(), dir: t.TempDir()}
	main := h.ok("skill")
	if !strings.HasPrefix(main.stdout, "---\nname: deployer\n") {
		t.Fatalf("deployer skill should print SKILL.md:\n%.200s", main.stdout)
	}
	for _, topic := range skillTopicNames() {
		var res struct{ Topic, Content string }
		h.ok("skill", topic, "--json").json(t, &res)
		if res.Topic != topic || !strings.HasPrefix(res.Content, "# ") {
			t.Fatalf("deployer skill %s --json = %+v", topic, res)
		}
	}
	if r := h.run("", "skill", "recipe"); r.code != ExitUsage || !strings.Contains(r.stderr, "recipes, commands") {
		t.Fatalf("unknown topic: %+v", r)
	}
}

// TestSkillLinks checks that every reference SKILL.md links to exists, and
// that `deployer skill` can print each one.
func TestSkillLinks(t *testing.T) {
	main, err := fs.ReadFile(skills.FS, "deployer/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, topic := range skillTopics {
		paths = append(paths, topic.path)
	}
	for _, m := range regexp.MustCompile(`\]\((references/[^)]+)\)`).FindAllStringSubmatch(string(main), -1) {
		if !slices.Contains(paths, "deployer/"+m[1]) {
			t.Errorf("SKILL.md links to %s, which deployer skill can't print", m[1])
		}
	}
	fs.WalkDir(skills.FS, "deployer", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !slices.Contains(paths, path) {
			t.Errorf("%s is not a topic of deployer skill", path)
		}
		return err
	})
}

// TestSkillCommandsExist keeps the skill in step with the CLI: every command
// and flag it mentions, in code spans and code blocks, must exist.
func TestSkillCommandsExist(t *testing.T) {
	root := (&app{env: Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Getenv: func(string) string { return "" }, Now: time.Now}}).rootCommand()
	commands := 0
	fs.WalkDir(skills.FS, "deployer", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(skills.FS, path)
		if err != nil {
			return err
		}
		for _, line := range skillCommandLines(string(data)) {
			commands++
			if err := checkCommandLine(root, line); err != "" {
				t.Errorf("%s: %q: %s", path, line, err)
			}
		}
		return nil
	})
	t.Logf("checked %d command lines", commands)
	if commands < 50 {
		t.Fatalf("found only %d deployer commands in the skill; is the parser broken?", commands)
	}
}

var codeSpan = regexp.MustCompile("`(deployer [^`]+)`")

// skillCommandLines returns the deployer command lines in markdown: whole
// lines in code blocks, and code spans elsewhere.
func skillCommandLines(markdown string) []string {
	var lines []string
	inBlock := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inBlock = !inBlock
		case inBlock:
			if strings.HasPrefix(trimmed, "deployer ") {
				lines = append(lines, trimmed)
			}
		default:
			for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
				lines = append(lines, m[1])
			}
		}
	}
	return lines
}

// checkCommandLine resolves the subcommands in line, such as "deployer
// operation wait ID --json", and checks its flags. It returns a problem, or "".
func checkCommandLine(root *cobra.Command, line string) string {
	if i := strings.Index(line, " #"); i >= 0 {
		line = line[:i] // A shell comment.
	}
	words := strings.Fields(strings.NewReplacer("[", " ", "]", " ", "\\", " ").Replace(line))[1:]
	cmd := root
	i := 0
	// Subcommands come first. "a|b" lists alternatives, and "..." stands for any.
	for ; i < len(words) && !strings.HasPrefix(words[i], "-"); i++ {
		w := words[i]
		if w == "..." {
			return ""
		}
		if alternatives := strings.Split(w, "|"); len(alternatives) > 1 {
			for _, alt := range alternatives {
				if sub := findSubcommand(cmd, alt); sub == nil {
					return "no command " + cmd.CommandPath() + " " + alt
				}
			}
			return ""
		}
		sub := findSubcommand(cmd, w)
		if sub == nil {
			if cmd == root || (cmd.Args != nil && cmd.Args(cmd, []string{w}) != nil) {
				return "no command " + cmd.CommandPath() + " " + w
			}
			if len(cmd.ValidArgs) > 0 && !slices.Contains(cmd.ValidArgs, w) && strings.ToUpper(w) != w {
				return w + " is not a valid argument of " + cmd.CommandPath()
			}
			break // An argument, such as a deployment name.
		}
		cmd = sub
	}
	for ; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") || w == "-" {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(w, "-"), "=")
		var found bool
		if strings.HasPrefix(w, "--") {
			found = cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil
		} else {
			found = cmd.Flags().ShorthandLookup(name) != nil || cmd.InheritedFlags().ShorthandLookup(name) != nil
		}
		if !found {
			return cmd.CommandPath() + " has no flag " + w
		}
	}
	return ""
}

func findSubcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, sub := range cmd.Commands() {
		if sub.Name() == name || sub.HasAlias(name) {
			return sub
		}
	}
	return nil
}

// The parser must catch mistakes, or the test above proves nothing.
func TestCheckCommandLine(t *testing.T) {
	root := (&app{env: Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Getenv: func(string) string { return "" }, Now: time.Now}}).rootCommand()
	for line, ok := range map[string]bool{
		"deployer deploy --ref BRANCH --ttl 1d --json":           true,
		"deployer list [--project NAME] [--all] --json":          true,
		"deployer operation wait ID --timeout 30m --json":        true,
		"deployer server install|upgrade|uninstall":              true,
		"deployer token ...":                                     true,
		"deployer destroy NAME -y --json  # comment --nope":      true,
		"deployer skill recipes":                                 true,
		"deployer deploy --branch main":                          false,
		"deployer operation wiat ID":                             false,
		"deployer deploymnet list":                               false,
		"deployer server install|upgrade|remove":                 false,
		"deployer skill recipe":                                  false,
		"deployer logs NAME -z":                                  false,
		"deployer inspect NAME --json --definitely-not-a-flag=1": false,
	} {
		if problem := checkCommandLine(root, line); (problem == "") != ok {
			t.Errorf("checkCommandLine(%q) = %q", line, problem)
		}
	}
}
