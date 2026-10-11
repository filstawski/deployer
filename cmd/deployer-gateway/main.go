package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"deployer/internal/gateway"
	"deployer/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version.Version)
		return
	}
	listen := flag.String("listen", "127.0.0.1:8080", "public gateway listen address")
	control := flag.String("control-listen", "127.0.0.1:8081", "authenticated control listener")
	tokenFile := flag.String("control-token-file", "", "gateway control token file")
	scheme := flag.String("public-scheme", "http", "external visitor scheme: http or https")
	flag.Parse()
	if *scheme != "http" && *scheme != "https" {
		fmt.Fprintln(os.Stderr, "invalid public scheme")
		os.Exit(1)
	}
	token := ""
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		token = strings.TrimSpace(string(b))
		if len(token) < 32 {
			fmt.Fprintln(os.Stderr, "control token too short")
			os.Exit(1)
		}
	}
	g := gateway.New(token)
	g.PublicScheme = *scheme
	s := &http.Server{Addr: *listen, Handler: g.Public(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	c := &http.Server{Addr: *control, Handler: g.Control(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = s.Shutdown(shutdown)
		_ = c.Shutdown(shutdown)
	}()
	if token != "" {
		go func() {
			if err := c.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintln(os.Stderr, err)
				cancel()
			}
		}()
	}
	fmt.Fprintln(os.Stderr, "Deployer gateway listening on", *listen)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
