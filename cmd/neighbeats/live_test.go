package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveTTY(t *testing.T) {
	var b bytes.Buffer
	l := &live{w: &b, tty: true}
	l.Status("a %d", 1)
	l.Logf("warn")
	l.last = time.Time{}
	l.Status("b")
	l.Done()
	got := b.String()
	if !strings.HasPrefix(got, "\r\x1b[Ka 1") || !strings.Contains(got, "\r\x1b[Kwarn\n") {
		t.Fatalf("got %q", got)
	}
	if strings.HasSuffix(got, "\n\n") || !strings.HasSuffix(got, "\r\x1b[K") {
		t.Fatalf("status line not cleared: %q", got)
	}
}

func TestLivePlain(t *testing.T) {
	var b bytes.Buffer
	l := &live{w: &b}
	l.Status("a")
	l.Status("b") // too soon, dropped
	l.Logf("c")
	if got := b.String(); got != "a\nc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"neighbeats-20260101-000000.000.sqlite", "neighbeats-20260102-000000.sqlite",
		"neighbeats-20260103-000000.sqlite", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, err := pruneBackups(dir, 2)
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "neighbeats-20260101-000000.000.sqlite")); err == nil {
		t.Fatal("oldest kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "other.txt")); err != nil {
		t.Fatal("unrelated file removed")
	}
}
