package cli

import (
	"context"
	"net/url"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
	"rsc.io/qr"
)

// pairing is what deployer pair prints with --json.
type pairing struct {
	domain.CreateTokenResponse
	// Server is the API address the phone connects to.
	Server string `json:"server"`
	// URL is the link in the QR code, which the app opens.
	URL string `json:"url"`
}

func (a *app) pairCommand() *cobra.Command {
	var role, ttl, server string
	var projects []string
	cmd := &cobra.Command{
		Use:   "pair [NAME]",
		Short: "Connect the Android app with a QR code (admin)",
		Long: `Create a token for a phone and show it as a QR code. In the Deployer app,
tap Scan QR code and point the camera at it. NAME is the token's name, "phone"
when omitted.

The code contains the token's secret: show it only to the phone it is for.
The phone connects to the server's public HTTPS address; --server sets
another one.`,
		Example: `  deployer pair
  deployer pair tablet --role viewer
  deployer pair --server https://api.previews.example.com`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := "phone"
			if len(args) == 1 {
				name = args[0]
			}
			req, err := tokenRequest(name, role, "", ttl)
			if err != nil {
				return err
			}
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			if server == "" {
				server = a.publicURL(ctx, c)
			}
			if err := client.ValidateURL(server, false); err != nil || !strings.HasPrefix(server, "https://") {
				return domain.Errorf(domain.CodeInvalidInput, "the phone needs the server's HTTPS address, and this connection doesn't tell it").
					WithHint("pass it: deployer pair --server https://api.previews.example.com")
			}
			if req.Projects, err = projectIDs(ctx, c, projects); err != nil {
				return err
			}
			created, err := c.CreateToken(ctx, req)
			if err != nil {
				return err
			}
			p := pairing{CreateTokenResponse: created, Server: strings.TrimRight(server, "/"), URL: pairURL(server, created.Secret)}
			code, err := qr.Encode(p.URL, qr.M)
			if err != nil {
				return err
			}
			return a.out.result(p, func() {
				st := a.out.st
				a.out.success("Created token %s (%s)", created.Token.Name, created.Token.ID)
				a.out.line("")
				for _, line := range qrLines(code, st.enabled) {
					a.out.line("  %s", st.paint(line, lipgloss.NewStyle().Foreground(lipgloss.Black).Background(lipgloss.BrightWhite)))
				}
				a.out.line("")
				a.out.line("Scan it in the Deployer app. Or enter it by hand:")
				a.out.line("  Address  %s", p.Server)
				a.out.line("  Token    %s", created.Secret)
				a.out.line("The secret is shown once. Revoke it with: %s", st.command("deployer token revoke "+created.Token.ID))
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&role, "role", "deployer", "token role: deployer, viewer, or admin")
	f.StringSliceVarP(&projects, "project", "p", nil, "limit the token to these projects (default: all)")
	f.StringVar(&ttl, "ttl", "90d", `lifetime, such as 30d, or "never"`)
	f.StringVar(&server, "server", "", "the API address the phone uses (default: the server's public address)")
	cmd.RegisterFlagCompletionFunc("role", completeFixed(domain.RoleNames()...))
	cmd.RegisterFlagCompletionFunc("project", a.completeProjects)
	return cmd
}

// publicURL returns the server's HTTPS address: the connection's own, or the
// one the server reports when the CLI connects over SSH or on the server.
func (a *app) publicURL(ctx context.Context, c *client.Client) string {
	if strings.HasPrefix(c.BaseURL(), "https://") {
		return c.BaseURL()
	}
	if s, err := c.Status(ctx); err == nil {
		return s.APIURL
	}
	return ""
}

// pairURL is the link the app opens: deployer://pair?server=...&token=...
func pairURL(server, secret string) string {
	q := url.Values{"server": {strings.TrimRight(server, "/")}, "token": {secret}}
	return "deployer://pair?" + q.Encode()
}

// qrLines draws a QR code with half blocks, two modules per character, inside
// the quiet zone scanners need. With color, the code is painted black on white
// and blocks are its dark modules. Without, blocks take the terminal's text
// color, which is light in most terminals, so they draw the light modules.
func qrLines(code *qr.Code, colored bool) []string {
	const quiet = 2
	size := code.Size + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	var lines []string
	for y := 0; y < size; y += 2 {
		var b strings.Builder
		for x := 0; x < size; x++ {
			top, bottom := dark(x, y) == colored, y+1 < size && dark(x, y+1) == colored
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}
