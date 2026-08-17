package log

import (
	"testing"
)

func TestBufferWritesSnapshotsAndNotifiesSubscribers(t *testing.T) {
	buffer := NewBuffer(2)
	updates, unsubscribe := buffer.Subscribe()
	defer unsubscribe()
	if _, err := buffer.Write([]byte("one\ntwo\nthree\n")); err != nil {
		t.Fatal(err)
	}
	lines := buffer.Snapshot()
	if len(lines) != 2 || lines[0] != "two" || lines[1] != "three" {
		t.Fatalf("Snapshot() = %v, want [two three]", lines)
	}
	for _, want := range []string{"one", "two", "three"} {
		if got := <-updates; got != want {
			t.Fatalf("subscription line = %q, want %q", got, want)
		}
	}
}

func TestBufferKeepsPartialLineUntilTerminated(t *testing.T) {
	buffer := NewBuffer(1)
	if _, err := buffer.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if len(buffer.Snapshot()) != 0 {
		t.Fatal("partial line was published")
	}
	if _, err := buffer.Write([]byte(" line\n")); err != nil {
		t.Fatal(err)
	}
	if lines := buffer.Snapshot(); len(lines) != 1 || lines[0] != "partial line" {
		t.Fatalf("Snapshot() = %v", lines)
	}
}
