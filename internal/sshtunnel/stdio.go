package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// openStdio forwards each HTTP connection through its own `ssh -W` process.
// Keep-alive usually leaves one connection, and so one ssh, per command.
func openStdio(t Target, apiPort int) *Tunnel {
	s := &stdio{target: t, args: t.args("-W", fmt.Sprintf("127.0.0.1:%d", apiPort)), conns: map[*pipeConn]bool{}}
	transport := &http.Transport{DialContext: s.dial, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}
	var once sync.Once
	return &Tunnel{Transport: reportFailures{transport}, close: func() {
		once.Do(func() {
			transport.CloseIdleConnections()
			s.closeAll()
		})
	}}
}

type stdio struct {
	target Target
	args   []string

	mu     sync.Mutex
	conns  map[*pipeConn]bool
	closed bool
}

func (s *stdio) dial(context.Context, string, string) (net.Conn, error) {
	// Plain pipes rather than cmd.StdoutPipe, whose Wait closes the pipe
	// and could drop the end of a response.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	// The connection outlives the request that opened it.
	procCtx, cancel := context.WithCancel(context.Background())
	cmd := command(procCtx, "ssh", s.args...)
	c := &pipeConn{owner: s, r: outR, w: inW, cancel: cancel, exited: make(chan struct{})}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, &c.stderr
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		cancel()
		inW.Close()
		outR.Close()
		return nil, fmt.Errorf("start ssh: %w", err)
	}
	go func() {
		if err := cmd.Wait(); err != nil && !c.closing.Load() {
			c.failed = s.target.failure(c.stderr.String())
		}
		close(c.exited)
	}()
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.conns[c] = true
	}
	s.mu.Unlock()
	if closed {
		c.Close()
		return nil, net.ErrClosed
	}
	return c, nil
}

func (s *stdio) closeAll() {
	s.mu.Lock()
	s.closed = true
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for c := range conns {
		c.Close()
	}
}

// pipeConn is a connection carried by one ssh process: requests go to its
// standard input, responses come from its standard output.
type pipeConn struct {
	owner  *stdio
	r, w   *os.File
	cancel context.CancelFunc
	stderr syncBuffer

	exited  chan struct{} // closed once ssh has exited
	failed  error         // why ssh failed, set before exited closes
	closing atomic.Bool
	once    sync.Once
}

func (c *pipeConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err != nil {
		err = c.failure(err)
	}
	return n, err
}

func (c *pipeConn) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if err != nil {
		err = c.failure(err)
	}
	return n, err
}

// failure replaces a pipe error with ssh's own, if ssh failed. ssh closes
// its output as it exits, so a short wait is enough.
func (c *pipeConn) failure(err error) error {
	select {
	case <-c.exited:
		if c.failed != nil {
			return c.failed
		}
	case <-time.After(2 * time.Second):
	}
	return err
}

// Close stops ssh. The connection is done with, so ssh needn't finish.
func (c *pipeConn) Close() error {
	c.once.Do(func() {
		c.closing.Store(true)
		c.w.Close()
		c.cancel()
		<-c.exited
		c.r.Close()
		c.owner.mu.Lock()
		delete(c.owner.conns, c)
		c.owner.mu.Unlock()
	})
	return nil
}

func (c *pipeConn) LocalAddr() net.Addr  { return sshAddr(c.owner.target.String()) }
func (c *pipeConn) RemoteAddr() net.Addr { return sshAddr(c.owner.target.String()) }

func (c *pipeConn) SetDeadline(t time.Time) error {
	return errors.Join(c.r.SetReadDeadline(t), c.w.SetWriteDeadline(t))
}
func (c *pipeConn) SetReadDeadline(t time.Time) error  { return c.r.SetReadDeadline(t) }
func (c *pipeConn) SetWriteDeadline(t time.Time) error { return c.w.SetWriteDeadline(t) }

type sshAddr string

func (a sshAddr) Network() string { return "ssh" }
func (a sshAddr) String() string  { return string(a) }

// reportFailures returns ssh's error, rather than a broken connection, when a
// request failed because ssh did.
type reportFailures struct{ *http.Transport }

func (t reportFailures) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	var failed *Error
	if errors.As(err, &failed) {
		return nil, failed
	}
	return resp, err
}

var _ net.Conn = (*pipeConn)(nil)
