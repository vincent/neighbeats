package main

import (
	"bytes"
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
