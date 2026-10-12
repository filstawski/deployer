package cli

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/filstawski/deployer/skills"
	"github.com/spf13/cobra"
)

// skillTopics lists the documents `deployer skill` prints, in the order the
// help shows them. The empty topic is the main guide.
var skillTopics = []struct{ name, path string }{
	{"", "deployer/SKILL.md"},
	{"recipes", "deployer/references/recipes.md"},
	{"commands", "deployer/references/commands.md"},
	{"troubleshooting", "deployer/references/troubleshooting.md"},
	{"concepts", "deployer/references/concepts.md"},
}

func skillTopicNames() []string {
	var names []string
	for _, t := range skillTopics[1:] {
		names = append(names, t.name)
	}
	return names
}

func (a *app) skillCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "skill [TOPIC]",
		Short: "Print the guide for AI agents",
		Long: `Print Deployer's guide for AI coding agents: how to prepare a project, deploy
a branch, manage previews, and fix failures. It matches this version of the CLI.

Without a topic, it prints the main guide. Topics:
  recipes          Writing deployer.yaml and the Dockerfile, with templates
  commands         Every command, its flags, and its JSON output
  troubleshooting  What each error means and how to fix it
  concepts         Lifecycle, states, limits, and access

To install it as a skill for Claude Code, Codex, Cursor, and other agents:
  npx skills add filstawski/deployer`,
		Example: `  deployer skill
  deployer skill recipes`,
		Args:      maxArgs(1),
		ValidArgs: skillTopicNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := ""
			if len(args) == 1 {
				topic = args[0]
			}
			for _, t := range skillTopics {
				if t.name != topic {
					continue
				}
				data, err := fs.ReadFile(skills.FS, t.path)
				if err != nil {
					return err
				}
				res := map[string]string{"topic": t.name, "content": string(data)}
				return a.out.result(res, func() { fmt.Fprint(a.out.w, res["content"]) })
			}
			return usage("unknown topic %q", topic).WithHint("choose one of: %s", strings.Join(skillTopicNames(), ", "))
		},
	}
}
