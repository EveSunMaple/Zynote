package api

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSummaryPairReservationsAreExclusive(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, "note.json")
	if err := os.WriteFile(orphan, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := make(chan string, 8)
	errors := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			path, sidecar, err := reserveSummaryPair(dir, "note")
			if err != nil {
				errors <- err
				return
			}
			if _, err = os.Stat(sidecar); err != nil {
				errors <- err
				return
			}
			paths <- path
		}()
	}
	workers.Wait()
	close(paths)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path := range paths {
		if seen[path] {
			t.Fatalf("duplicate reservation: %s", path)
		}
		seen[path] = true
	}
	if len(seen) != 8 {
		t.Fatalf("got %d pairs", len(seen))
	}
	if body, err := os.ReadFile(orphan); err != nil || string(body) != "original" {
		t.Fatalf("orphan changed: %q %v", body, err)
	}
}
