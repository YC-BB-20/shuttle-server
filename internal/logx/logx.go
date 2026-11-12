// Package logx writes the program's own log: one folder per month under logs,
// appended, never trimmed or deleted. Never put secrets in a log line.
package logx

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Open returns a logger writing to stdout and logs/YYYY-MM/shuttle.log.
func Open(logsDir string, stdout io.Writer, now time.Time) (*slog.Logger, func(), error) {
	dir := filepath.Join(logsDir, now.Format("2006-01"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "shuttle.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	l := slog.New(slog.NewTextHandler(io.MultiWriter(stdout, f), nil))
	return l, func() { f.Close() }, nil
}
