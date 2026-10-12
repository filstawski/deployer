package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	"github.com/filstawski/deployer/internal/domain"
)

// LogSource selects which log to read.
type LogSource string

const (
	LogsApp   LogSource = "app"
	LogsBuild LogSource = "build"
)

// LogsRequest selects part of a log.
type LogsRequest struct {
	Source LogSource
	// Lines is the number of trailing lines to return.
	Lines int
	// Offset, when non-negative, reads the build log from this byte offset
	// instead, for following a build in progress.
	Offset int64
}

const maxLogChunk = 1 << 20

// Logs returns application or build output.
func (c *Controller) Logs(ctx context.Context, d domain.Deployment, req LogsRequest) (domain.LogsResponse, error) {
	if req.Lines <= 0 || req.Lines > 10000 {
		req.Lines = 200
	}
	if req.Source == LogsApp {
		out, err := c.runtime.Logs(ctx, d.ID, req.Lines)
		return domain.LogsResponse{Logs: out}, err
	}
	f, err := os.Open(c.buildLog(d.ID))
	if errors.Is(err, os.ErrNotExist) {
		return domain.LogsResponse{}, domain.Errorf(domain.CodeNotFound, "%s has no build log yet", d.Name)
	}
	if err != nil {
		return domain.LogsResponse{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return domain.LogsResponse{}, err
	}
	size := info.Size()
	if req.Offset >= 0 {
		offset := min(req.Offset, size)
		chunk := make([]byte, min(size-offset, maxLogChunk))
		n, err := f.ReadAt(chunk, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return domain.LogsResponse{}, err
		}
		return domain.LogsResponse{Logs: string(chunk[:n]), Offset: offset + int64(n)}, nil
	}
	start := max(0, size-maxLogChunk)
	tail := make([]byte, size-start)
	if _, err := f.ReadAt(tail, start); err != nil && !errors.Is(err, io.EOF) {
		return domain.LogsResponse{}, err
	}
	return domain.LogsResponse{Logs: string(lastLines(tail, req.Lines)), Offset: size}, nil
}

func lastLines(b []byte, n int) []byte {
	b = bytes.TrimRight(b, "\n")
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			if n--; n == 0 {
				return append(b[i+1:], '\n')
			}
		}
	}
	if len(b) == 0 {
		return b
	}
	return append(b, '\n')
}
