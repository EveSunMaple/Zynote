package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

func TestSubtitleDerivedResumeQueuesBehindExistingJob(t *testing.T) {
	inv := NewInvoker()
	root := t.TempDir()
	state := &contract.State{LibraryRoot: filepath.Join(root, "Library"), DataRoot: filepath.Join(root, "Data"), ConfigRoot: filepath.Join(root, "Config")}
	if err := os.MkdirAll(state.LibraryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "1\n00:00:00,000 --> 00:00:02,000\nHello\n"
	path := filepath.Join(state.LibraryRoot, "source.srt")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(text))
	task, err := subtitleDerivedService(state).Create([]subtitlederived.Source{{URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", Title: "Queue test", Path: path, Hash: hex.EncodeToString(hash[:]), Generation: "unavailable-version"}}, []string{"brief"}, "Course")
	if err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = inv.jobs.Run("existing", "", func(context.Context) (any, error) { close(entered); <-release; return nil, nil })
	}()
	<-entered
	defer func() { close(release); <-finished }()
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	result := make(chan error, 1)
	go func() { _, err := inv.handleSubtitleDerivedResume(nil, state, params); result <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if record, err := inv.jobs.QueueRecord(filepath.Join(state.LibraryRoot, "TaskQueue"), task.ID); err == nil && record.State == "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("API did not persist queue admission")
		}
		time.Sleep(time.Millisecond)
	}
	value, err := inv.handleSubtitleDerivedList(state)
	if err != nil {
		t.Fatal(err)
	}
	rows := value.([]subtitlederived.Task)
	if len(rows) != 1 || rows[0].QueueState != "queued" {
		t.Fatalf("missing UI projection: %+v", rows)
	}
	// Cancel the waiting request without executing a model or touching real state.
	inv.jobs.CancelAll()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled queued API succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("API cancellation stuck")
	}
}
