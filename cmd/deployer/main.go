// Command deployer is the Deployer CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/filstawski/deployer/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, cli.ProcessEnv(), os.Args[1:])
	stop()
	os.Exit(code)
}
