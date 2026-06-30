package audit

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-logr/logr"
)

// TestRecordCloseNoRaceOrPanic exercises the shutdown path: many concurrent
// Record calls racing a Close. Before the fix, Record read l.file without the
// lock and writeFile could dereference a nil *os.File after Close nilled it.
// Run with -race to catch the data race; the nil-deref would panic regardless.
func TestRecordCloseNoRaceOrPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := New(logr.Discard(), nil, false, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// obj is nil so no Kubernetes Event is attempted; the file write path
			// is what we want to stress.
			l.Record(nil, "Normal", ActionNodeDrained, "node drained", map[string]string{"node": "n1"})
		}()
	}

	// Close while writers are still in flight.
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wg.Wait()

	// Records after Close must be safe no-ops, not panics.
	l.Record(nil, "Normal", ActionCompleted, "done", nil)
}

func TestRecordWithoutFileIsSafe(t *testing.T) {
	l, err := New(logr.Discard(), nil, false, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l.fileConfigured {
		t.Fatal("fileConfigured must be false when no export path is given")
	}
	l.Record(nil, "Normal", ActionCreated, "created", nil)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
