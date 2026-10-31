package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

func TestVideoSummaryHandlerQueuedCancellationRetainsSnapshot(t *testing.T) {
	inv := NewInvoker()
	state := &contract.State{LibraryRoot: t.TempDir()}
	holdQueueWorker(t, inv)
	id := "abababab-abab-4bab-8bab-abababababab"
	params, _ := json.Marshal(map[string]string{"operationId": id, "mode": "brief", "title": "Lecture", "srt": "1\n00:00:00,000 --> 00:00:02,000\nHello\n"})
	result := make(chan error, 1)
	go func() { _, err := inv.handleVideoSummary(nil, state, params); result <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if record, err := inv.jobs.QueueRecord(filepath.Join(state.LibraryRoot, "TaskQueue"), id); err == nil && record.State == "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("summary was not queued")
		}
		time.Sleep(time.Millisecond)
	}
	inv.jobs.CancelAll()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled summary succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel stalled")
	}
	task, err := subtitleDerivedService(state).Read(id)
	if err != nil || task.Items[0].GenerationState != "" {
		t.Fatal("cancelled waiting task dispatched provider")
	}
	if _, err := os.ReadFile(task.Items[0].Source.Path); err != nil {
		t.Fatal("cancel lost snapshot")
	}
}

func TestVideoSummaryHandlerJournalRecovery(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared", true: "unknown"}[unknown], func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			srt := "1\n00:00:00,000 --> 00:00:02,000\nHello\n"
			task, err := service.CreateDirectOperation(srt, "Lecture", "brief", "", id)
			if err != nil {
				t.Fatal(err)
			}
			task.Items[0].GenerationState = "dispatched"
			if !unknown {
				task.Items[0].Prepared = &subtitlederived.Prepared{Markdown: "# Saved response", Points: []subtitlederived.Point{{Cue: 0, Quote: "Hello", Point: "Greeting", Timestamp: "00:00:00"}}}
			}
			encoded, _ := json.Marshal(task)
			if err := os.WriteFile(filepath.Join(service.Root, id+".json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			params, _ := json.Marshal(map[string]string{"operationId": id, "srt": srt, "title": "Lecture", "mode": "brief"})
			// No provider/store: a model call cannot succeed in this recovery test.
			inv := NewInvoker()
			for range 2 {
				value, err := inv.handleVideoSummary(nil, state, params)
				if unknown {
					if err == nil || !strings.Contains(err.Error(), "未知") {
						t.Fatalf("unknown result accepted: %v", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				result := value.(map[string]any)
				if result["count"] != 1 || !strings.Contains(result["markdown"].(string), "传入 SRT 快照") {
					t.Fatalf("invalid adapter result %+v", result)
				}
			}
			conflict, _ := json.Marshal(map[string]string{"operationId": id, "srt": srt + "changed", "title": "Lecture", "mode": "brief"})
			if _, err := inv.handleVideoSummary(nil, state, conflict); err == nil {
				t.Fatal("changed subtitle accepted")
			}
			records, err := service.List()
			if err != nil || len(records) != 1 {
				t.Fatal("retry duplicated task")
			}
		})
	}
}
