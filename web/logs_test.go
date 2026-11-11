package main

import (
	"strings"
	"testing"
	"time"
)

func TestMemoryLogWriteAndLimit(t *testing.T) {
	m := newMemoryLog(3)
	for _, s := range []string{"one\n", "two\n", "thr", "ee\n", "four\n"} {
		if _, err := m.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	history, ch := m.subscribe()
	defer m.unsubscribe(ch)

	want := []string{"two", "three", "four"}
	if len(history) != len(want) {
		t.Fatalf("history = %v, want %v", history, want)
	}
	for i := range want {
		if history[i] != want[i] {
			t.Errorf("history[%d] = %q, want %q", i, history[i], want[i])
		}
	}
}

func TestMemoryLogNoTrailingNewline(t *testing.T) {
	m := newMemoryLog(10)
	if _, err := m.Write([]byte("boot\nconfig")); err != nil {
		t.Fatal(err)
	}

	history, ch := m.subscribe()
	defer m.unsubscribe(ch)
	if len(history) != 1 || history[0] != "boot" {
		t.Fatalf("history = %v, want [boot]", history)
	}

	m.flushPartial()
	history, ch2 := m.subscribe()
	defer m.unsubscribe(ch2)
	if len(history) != 2 || history[1] != "config" {
		t.Fatalf("after flush = %v, want [boot config]", history)
	}
}

func TestMemoryLogSubscribeAtomic(t *testing.T) {
	m := newMemoryLog(10)
	m.Write([]byte("old\n"))

	history, ch := m.subscribe()
	defer m.unsubscribe(ch)
	if len(history) != 1 || history[0] != "old" {
		t.Fatalf("history = %v, want [old]", history)
	}

	// A line written after subscribing must arrive on the channel, not in the
	// already-returned snapshot.
	m.Write([]byte("new\n"))
	select {
	case line := <-ch:
		if line != "new" {
			t.Errorf("live line = %q, want new", line)
		}
	default:
		t.Fatal("no live line delivered")
	}
}

func TestMemoryLogSetLimit(t *testing.T) {
	m := newMemoryLog(100)
	for i := 0; i < 100; i++ {
		m.Write([]byte(strings.Repeat("x", i+1) + "\n"))
	}
	m.setLimit(16)

	history, ch := m.subscribe()
	defer m.unsubscribe(ch)
	if len(history) != 16 {
		t.Fatalf("history length = %d, want 16", len(history))
	}
	if history[15] != strings.Repeat("x", 100) {
		t.Errorf("last line = %q", history[15])
	}
}

// setLimit clamps below the supported minimum, so a smaller value cannot be
// applied by accident.
func TestMemoryLogSetLimitClamps(t *testing.T) {
	m := newMemoryLog(100)
	for i := 0; i < 20; i++ {
		m.Write([]byte("line\n"))
	}
	m.setLimit(4)
	if got := m.len(); got != 16 {
		t.Errorf("retained = %d, want 16", got)
	}
}

func TestRecordStateISO(t *testing.T) {
	recordState("started (pid 123)", "unit test reason")

	history, ch := stateLog.subscribe()
	defer stateLog.unsubscribe(ch)
	if len(history) == 0 {
		t.Fatal("state log is empty")
	}

	line := history[len(history)-1]
	parts := strings.SplitN(line, " ", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected line: %q", line)
	}

	const layout = "2006-01-02T15:04:05-07:00"
	if _, err := time.Parse(layout, parts[0]); err != nil {
		t.Fatalf("timestamp %q is not ISO-8601 with offset: %v", parts[0], err)
	}
	if len(parts[0]) != len("2006-01-02T15:04:05+08:00") {
		t.Fatalf("timestamp %q has unexpected length", parts[0])
	}
	off := parts[0][len(parts[0])-6:]
	if off[0] != '+' && off[0] != '-' {
		t.Errorf("offset %q does not start with +/-", off)
	}
	if off[3] != ':' {
		t.Errorf("offset %q is not numeric (+hh:mm)", off)
	}
	if !strings.Contains(parts[1], "unit test reason") {
		t.Errorf("reason missing from %q", line)
	}
}

func TestClampLogLines(t *testing.T) {
	cases := map[int]int{
		0:    minLogLines,
		1:    minLogLines,
		16:   16,
		128:  128,
		4000: 4000,
		5000: maxLogLines,
		-100: minLogLines,
	}
	for in, want := range cases {
		if got := clampLogLines(in); got != want {
			t.Errorf("clampLogLines(%d) = %d, want %d", in, got, want)
		}
	}
}
