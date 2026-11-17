package lucid

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDirty(t *testing.T) {
	for out, want := range map[string]int64{
		"dirtyBytes: 0\n":            0,
		"dirtyBytes 1234":            1234,
		"DirtyBytes = 1,048,576":     1 << 20,
		"dirtyBytes: 1.5 GiB":        3 << 29,
		"cache\n  dirtyBytes  2 GB":  2e9,
		"0\n":                        0,
		"  52428800  \n":             52428800,
		"Cache:\ndirtyBytes: 77 B\n": 77,
	} {
		got, ok := parseDirty(out)
		if !ok || got != want {
			t.Errorf("%q: got %d %v, want %d", out, got, ok, want)
		}
	}
	for _, out := range []string{"", "error: not linked", "12\n34\n", "dirty", "1.5\n", "dirtyBytes: lots"} {
		if n, ok := parseDirty(out); ok {
			t.Errorf("%q: read as %d, should not be understood", out, n)
		}
	}
}

func TestClientNeverGuesses(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Client{Set: "/nonexistent/lucid3"}).Waiting(ctx); !errors.Is(err, ErrNoCommand) {
		t.Fatalf("missing command: %v", err)
	}
	var got [][]string
	c := &Client{Cmd: "/x/lucid3", Run: func(_ context.Context, cmd string, args ...string) (string, error) {
		got = append(got, args)
		if args[0] == "perf" {
			return "something new\n", nil
		}
		return "", errors.New("exit status 1")
	}}
	if _, err := c.Waiting(ctx); err == nil || !strings.Contains(err.Error(), "something new") {
		t.Fatalf("unreadable answer: %v", err)
	}
	if err := c.Drop(ctx); err == nil {
		t.Fatal("a failed drop must be an error")
	}
	if strings.Join(got[0], " ") != "perf --cache dirtyBytes" || strings.Join(got[1], " ") != "cache --drop" {
		t.Fatalf("commands %v", got)
	}
}

func TestFindAndRun(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "lucid3")
	os.WriteFile(p, []byte("#!/bin/sh\necho \"dirtyBytes: 42\"\n"), 0o755)
	if Find(p) != p || Find(filepath.Join(dir, "none")) != "" {
		t.Fatal("Find with a set command")
	}
	c := New(p)
	if n, err := c.Waiting(context.Background()); err != nil || n != 42 {
		t.Fatalf("%d %v", n, err)
	}
	os.WriteFile(p, []byte("#!/bin/sh\nsleep 60\n"), 0o755)
	old := Timeout
	t.Cleanup(func() { setTimeout(old) })
	setTimeout(200e6)
	if _, err := c.Waiting(context.Background()); err == nil || !strings.Contains(err.Error(), "in time") {
		t.Fatalf("hung command: %v", err)
	}
}
