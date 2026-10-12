package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// live writes log lines and one status line that is rewritten in place when
// the output is a terminal. Anywhere else (a pipe, a log file, docker without
// a tty) the status becomes a plain line, at most one every few seconds.
type live struct {
	mu     sync.Mutex
	w      io.Writer
	tty    bool
	active bool // a status line is showing and has no newline yet
	last   time.Time
}

func newLive(w io.Writer) *live {
	l := &live{w: w}
	if f, ok := w.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			l.tty = true
		}
	}
	return l
}

func (l *live) every() time.Duration {
	if l.tty {
		return 100 * time.Millisecond
	}
	return 5 * time.Second
}

// Logf prints a permanent line, above the status line if there is one.
func (l *live) Logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clear()
	fmt.Fprintf(l.w, f+"\n", a...)
}

// Status shows the current progress; calls that come too soon are dropped.
func (l *live) Status(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := time.Now(); now.Sub(l.last) < l.every() {
		return
	} else {
		l.last = now
	}
	if !l.tty {
		fmt.Fprintf(l.w, f+"\n", a...)
		return
	}
	fmt.Fprintf(l.w, "\r\x1b[K"+f, a...)
	l.active = true
}

// Done ends the status line so that later output starts on a fresh line.
func (l *live) Done() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clear()
}

// clear erases the status line; the next Status redraws it.
func (l *live) clear() {
	if l.active {
		fmt.Fprint(l.w, "\r\x1b[K")
		l.active = false
	}
}
