package cli

import (
	"context"
	"deployer/internal/config"
	"deployer/internal/install"
	"deployer/internal/sshtunnel"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"deployer/internal/client"
	"deployer/internal/credentials"
	"deployer/internal/domain"
	"deployer/internal/store/sqlite"
	"deployer/internal/version"
)

const help = `Deployer — manage branch preview infrastructure

Available now:
  setup --domain DOMAIN --bundle DIR [--tls-cert FILE --tls-key FILE --dry-run]
  server add NAME --url URL --credential-file FILE
  server add NAME --ssh USER@HOST --credential-file FILE [--remote-port 7420]
  server use NAME
  server list
  server status
  project add NAME --repo URL [--idempotency-key KEY]
  project list
  project inspect NAME_OR_ID
  project verify NAME_OR_ID [--wait --timeout 120s --idempotency-key KEY]
  deploy PROJECT --ref BRANCH [--ttl 1d --subdomain NAME --wait]
  list
  inspect ID
  logs ID [--build]
  stop|wake ID [--wait]
  extend ID --by 1d [--wait]
  destroy ID --yes [--wait]
  operation inspect ID [--wait --timeout 120s]
  token create NAME --output FILE --scopes SCOPES --projects IDS [--ttl 720h]
  token list
  token revoke ID
  version

Global options: --json, --server URL, --token-file FILE, --contexts-file FILE
Environment: DEPLOYER_SERVER, DEPLOYER_TOKEN_FILE
Deployment commands accept --idempotency-key KEY and --wait --timeout DURATION.
Idle sleep and private repositories are not implemented yet.
`

type options struct {
	JSON                         bool
	URL, TokenFile, ContextsFile string
}
type app struct {
	out, errOut     io.Writer
	opts            options
	connection      *client.Client
	closeConnection func()
}

func Run(ctx context.Context, args []string, out, errOut io.Writer) int {
	home, _ := os.UserConfigDir()
	a := &app{out: out, errOut: errOut, opts: options{URL: os.Getenv("DEPLOYER_SERVER"), TokenFile: os.Getenv("DEPLOYER_TOKEN_FILE"), ContextsFile: filepath.Join(home, "deployer", "contexts.json")}}
	defer func() {
		if a.closeConnection != nil {
			a.closeConnection()
		}
	}()
	rest, err := a.globals(args)
	var result any
	if err == nil {
		result, err = a.execute(ctx, rest)
	}
	if err != nil {
		var e *domain.Error
		if !errors.As(err, &e) {
			e = domain.Fail("CLI_ERROR", err.Error())
		}
		if a.opts.JSON {
			_ = json.NewEncoder(out).Encode(domain.Envelope{SchemaVersion: 1, Error: e})
		} else {
			fmt.Fprintln(errOut, e.Error())
		}
		switch e.Code {
		case "UNAUTHENTICATED", "FORBIDDEN":
			return 3
		case "WAIT_TIMEOUT":
			return 4
		case "INVALID_INPUT", "UNSUPPORTED_FEATURE":
			return 2
		}
		return 1
	}
	if result != nil {
		if a.opts.JSON {
			_ = json.NewEncoder(out).Encode(domain.Envelope{SchemaVersion: 1, Data: result})
		} else {
			b, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(out, string(b))
		}
	}
	return 0
}
func (a *app) globals(args []string) ([]string, error) {
	out := []string{}
	for i := 0; i < len(args); i++ {
		v := args[i]
		if v == "--json" {
			a.opts.JSON = true
			continue
		}
		key, value, hasValue := strings.Cut(v, "=")
		var target *string
		switch key {
		case "--server":
			target = &a.opts.URL
		case "--token-file":
			target = &a.opts.TokenFile
		case "--contexts-file":
			target = &a.opts.ContextsFile
		}
		if target == nil {
			out = append(out, v)
			continue
		}
		if !hasValue {
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "--") {
				return nil, domain.Fail("INVALID_INPUT", key+" requires a value")
			}
			value = args[i]
		}
		*target = value
	}
	return out, nil
}
func (a *app) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(a.errOut)
	return f
}
func parse(f *flag.FlagSet, args []string) error {
	if err := f.Parse(args); err != nil {
		return domain.Fail("INVALID_INPUT", err.Error())
	}
	if f.NArg() != 0 {
		return domain.Fail("INVALID_INPUT", "unexpected positional arguments")
	}
	return nil
}
func (a *app) connect(ctx context.Context) (*client.Client, error) {
	if a.connection != nil {
		return a.connection, nil
	}
	u, t := a.opts.URL, a.opts.TokenFile
	var sshTarget string
	var remotePort int
	if u == "" || t == "" {
		c, err := loadContexts(a.opts.ContextsFile)
		if err != nil {
			return nil, err
		}
		if current, ok := c.Servers[c.Current]; ok {
			if u != "" && t == "" && (current.SSH != "" || strings.TrimRight(u, "/") != strings.TrimRight(current.URL, "/")) {
				return nil, domain.Fail("UNAUTHENTICATED", "changing the server URL requires an explicit --token-file; credentials are bound to their saved server")
			}
			if u == "" {
				if err := current.validate(); err != nil {
					return nil, err
				}
				u = current.URL
				sshTarget, remotePort = current.SSH, current.RemotePort
			}
			if t == "" {
				t = current.TokenFile
			}
		}
	}
	if u == "" && t == "" && runtime.GOOS == "linux" && os.Geteuid() == 0 {
		if c, e := config.Load("/etc/deployer/server.yaml"); e == nil {
			if _, e = os.Stat("/var/lib/deployer/operator.token"); e == nil {
				u = "http://" + c.Listen
				t = "/var/lib/deployer/operator.token"
			}
		}
	}
	if u == "" {
		u = "http://127.0.0.1:7420"
	}
	if t == "" {
		return nil, domain.Fail("UNAUTHENTICATED", "provide --token-file or add a server context")
	}
	secret, err := credentials.Read(t)
	if err != nil {
		return nil, err
	}
	c, err := client.New(u, secret)
	if err != nil {
		return nil, err
	}
	if sshTarget != "" {
		transport, cleanup, err := sshtunnel.Open(ctx, sshTarget, remotePort)
		if err != nil {
			return nil, err
		}
		c.HTTP.Transport = transport
		a.closeConnection = cleanup
	}
	a.connection = c
	return c, nil
}
func (a *app) call(ctx context.Context, method, path, key string, input any) (json.RawMessage, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	r, err := c.Do(ctx, method, path, key, input)
	return r.Data, err
}
func (a *app) execute(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		if a.opts.JSON {
			return map[string]string{"help": help}, nil
		}
		fmt.Fprint(a.out, help)
		return nil, nil
	}
	if args[0] == "version" {
		return map[string]string{"version": version.Version}, nil
	}
	switch args[0] {
	case "setup":
		return a.setup(ctx, args[1:])
	case "deploy":
		return a.deploy(ctx, args[1:])
	case "list", "inspect", "logs", "stop", "wake", "extend", "destroy":
		return a.deployment(ctx, args[0], args[1:])
	}
	if len(args) < 2 {
		return nil, domain.Fail("INVALID_INPUT", "expected a subcommand; run deployer help")
	}
	switch args[0] {
	case "server":
		return a.server(ctx, args[1:])
	case "project":
		return a.project(ctx, args[1:])
	case "operation":
		return a.operation(ctx, args[1:])
	case "token":
		return a.token(ctx, args[1:])
	}
	return nil, domain.Fail("UNSUPPORTED_FEATURE", "command is not implemented; run deployer help for available commands")
}
func (a *app) server(ctx context.Context, args []string) (any, error) {
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return nil, domain.Fail("INVALID_INPUT", "server status takes no arguments")
		}
		return a.call(ctx, "GET", "/v1/server", "", nil)
	case "list":
		if len(args) != 1 {
			return nil, domain.Fail("INVALID_INPUT", "server list takes no arguments")
		}
		return loadContexts(a.opts.ContextsFile)
	case "use":
		if len(args) != 2 {
			return nil, domain.Fail("INVALID_INPUT", "server use requires a name")
		}
		c, err := loadContexts(a.opts.ContextsFile)
		if err != nil {
			return nil, err
		}
		if _, ok := c.Servers[args[1]]; !ok {
			return nil, domain.Fail("NOT_FOUND", "server context not found")
		}
		c.Current = args[1]
		if err = saveContexts(a.opts.ContextsFile, c); err != nil {
			return nil, err
		}
		return map[string]string{"current": args[1]}, nil
	case "add":
		if len(args) < 2 {
			return nil, domain.Fail("INVALID_INPUT", "server add requires a name")
		}
		f := a.flags("server add")
		u := f.String("url", "", "management API URL")
		sshTarget := f.String("ssh", "", "SSH target or config alias")
		remotePort := f.Int("remote-port", 0, "remote loopback API port (SSH default 7420)")
		t := f.String("credential-file", "", "existing API token file")
		if err := parse(f, args[2:]); err != nil {
			return nil, err
		}
		if *t == "" {
			return nil, domain.Fail("INVALID_INPUT", "--credential-file is required")
		}
		if _, err := credentials.Read(*t); err != nil {
			return nil, err
		}
		portSet := false
		f.Visit(func(v *flag.Flag) {
			if v.Name == "remote-port" {
				portSet = true
			}
		})
		if *sshTarget != "" && !portSet {
			*remotePort = 7420
		}
		if *sshTarget == "" && portSet {
			return nil, domain.Fail("INVALID_INPUT", "--remote-port requires --ssh")
		}
		if *u == "" && *sshTarget == "" {
			*u = "http://127.0.0.1:7420"
		}
		connection := Connection{URL: *u, SSH: *sshTarget, RemotePort: *remotePort, TokenFile: *t}
		if err := addContext(a.opts.ContextsFile, args[1], connection); err != nil {
			return nil, err
		}
		return map[string]any{"name": args[1], "connection": connection}, nil
	}
	return nil, domain.Fail("INVALID_INPUT", "unknown server command")
}
func (a *app) project(ctx context.Context, args []string) (any, error) {
	if args[0] == "list" {
		if len(args) != 1 {
			return nil, domain.Fail("INVALID_INPUT", "project list takes no arguments")
		}
		return a.call(ctx, "GET", "/v1/projects", "", nil)
	}
	if len(args) < 2 {
		return nil, domain.Fail("INVALID_INPUT", "project command requires a name or ID")
	}
	id := args[1]
	switch args[0] {
	case "inspect":
		if len(args) != 2 {
			return nil, domain.Fail("INVALID_INPUT", "project inspect takes only a name or ID")
		}
		return a.call(ctx, "GET", "/v1/projects/"+url.PathEscape(id), "", nil)
	case "add":
		f := a.flags("project add")
		repo := f.String("repo", "", "Git repository URL")
		key := f.String("idempotency-key", "", "retry identity")
		if err := parse(f, args[2:]); err != nil {
			return nil, err
		}
		return a.call(ctx, "POST", "/v1/projects", *key, map[string]string{"name": id, "repository": *repo})
	case "verify":
		f := a.flags("project verify")
		wait := f.Bool("wait", false, "wait for completion")
		timeout := f.Duration("timeout", 120*time.Second, "client wait timeout")
		key := f.String("idempotency-key", "", "retry identity")
		if err := parse(f, args[2:]); err != nil {
			return nil, err
		}
		if *timeout <= 0 {
			return nil, domain.Fail("INVALID_INPUT", "timeout must be positive")
		}
		if *key == "" {
			*key = domain.ID("request")
		}
		data, err := a.call(ctx, "POST", "/v1/projects/"+url.PathEscape(id)+"/verify", *key, struct{}{})
		if err != nil {
			return nil, err
		}
		var op sqlite.Operation
		if err = json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		if !*wait {
			return op, nil
		}
		return a.wait(ctx, op.ID, *timeout)
	}
	return nil, domain.Fail("INVALID_INPUT", "unknown project command")
}
func (a *app) operation(ctx context.Context, args []string) (any, error) {
	if len(args) < 2 || args[0] != "inspect" {
		return nil, domain.Fail("INVALID_INPUT", "operation inspect requires an operation ID")
	}
	f := a.flags("operation inspect")
	wait := f.Bool("wait", false, "wait for completion")
	timeout := f.Duration("timeout", 120*time.Second, "client wait timeout")
	if err := parse(f, args[2:]); err != nil {
		return nil, err
	}
	if *timeout <= 0 {
		return nil, domain.Fail("INVALID_INPUT", "timeout must be positive")
	}
	if *wait {
		return a.wait(ctx, args[1], *timeout)
	}
	return a.call(ctx, "GET", "/v1/operations/"+url.PathEscape(args[1]), "", nil)
}
func (a *app) wait(ctx context.Context, id string, timeout time.Duration) (any, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		data, err := a.call(waitCtx, "GET", "/v1/operations/"+url.PathEscape(id), "", nil)
		if waitCtx.Err() != nil {
			return nil, domain.Fail("WAIT_TIMEOUT", "client stopped waiting; operation "+id+" continues on the server (inspect it to resume)")
		}
		if err != nil {
			return nil, err
		}
		var op sqlite.Operation
		if err = json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		if op.Status == "succeeded" {
			return op, nil
		}
		if op.Status == "failed" {
			if op.Error != nil {
				return nil, &domain.Error{Code: op.Error.Code, Message: op.Error.Message + " (operation " + id + ")", Retryable: op.Error.Retryable}
			}
			return nil, domain.Fail("OPERATION_FAILED", "operation "+id+" failed")
		}
		select {
		case <-waitCtx.Done():
			return nil, domain.Fail("WAIT_TIMEOUT", "client stopped waiting; operation "+id+" continues on the server")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (a *app) token(ctx context.Context, args []string) (any, error) {
	if args[0] == "list" {
		if len(args) != 1 {
			return nil, domain.Fail("INVALID_INPUT", "token list takes no arguments")
		}
		return a.call(ctx, "GET", "/v1/tokens", "", nil)
	}
	if args[0] == "revoke" {
		if len(args) != 2 {
			return nil, domain.Fail("INVALID_INPUT", "token revoke requires an ID")
		}
		return a.call(ctx, "DELETE", "/v1/tokens/"+url.PathEscape(args[1]), "", nil)
	}
	if args[0] != "create" || len(args) < 2 {
		return nil, domain.Fail("INVALID_INPUT", "token create requires a name")
	}
	f := a.flags("token create")
	output := f.String("output", "", "new owner-only credential file")
	scopes := f.String("scopes", "projects:read", "comma-separated scopes")
	projects := f.String("projects", "", "comma-separated project IDs or *")
	ttl := f.Duration("ttl", 720*time.Hour, "token lifetime")
	if err := parse(f, args[2:]); err != nil {
		return nil, err
	}
	if *projects == "" {
		return nil, domain.Fail("INVALID_INPUT", "--projects must explicitly grant project IDs or *")
	}
	file, err := credentials.Create(*output)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	success := false
	defer func() {
		if !success {
			os.Remove(*output)
		}
	}()
	data, err := a.call(ctx, "POST", "/v1/tokens", "", sqlite.TokenInput{Name: args[1], Scopes: strings.Split(*scopes, ","), Projects: strings.Split(*projects, ","), TTLSeconds: int64(ttl.Seconds())})
	if err != nil {
		return nil, err
	}
	var issued struct {
		Token  domain.Token `json:"token"`
		Secret string       `json:"secret"`
	}
	if err = json.Unmarshal(data, &issued); err != nil {
		return nil, err
	}
	if err = credentials.Write(file, issued.Secret); err != nil {
		_, _ = a.call(ctx, "DELETE", "/v1/tokens/"+url.PathEscape(issued.Token.ID), "", nil)
		return nil, fmt.Errorf("cannot save token %s; attempted revocation: %w", issued.Token.ID, err)
	}
	success = true
	return map[string]any{"token": issued.Token, "saved_to": *output}, nil
}

func (a *app) setup(ctx context.Context, args []string) (any, error) {
	f := a.flags("setup")
	o := install.Options{Version: version.Version}
	f.StringVar(&o.Domain, "domain", "", "preview base domain")
	f.StringVar(&o.Bundle, "bundle", "", "directory containing released binaries")
	f.StringVar(&o.Cert, "tls-cert", "", "existing wildcard certificate PEM for nginx")
	f.StringVar(&o.Key, "tls-key", "", "matching private key PEM")
	f.IntVar(&o.APIPort, "api-port", 7420, "loopback management port")
	f.IntVar(&o.GatewayPort, "gateway-port", 8080, "loopback preview ingress port")
	f.IntVar(&o.ControlPort, "control-port", 7421, "loopback gateway control port")
	f.BoolVar(&o.DryRun, "dry-run", false, "print plan without checking or modifying the host")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	return install.New(a.errOut).Apply(ctx, o)
}
