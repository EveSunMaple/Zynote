package visual

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageTimes(t *testing.T) {
	times := coverageTimes(22, 12)
	if len(times) != 5 {
		t.Fatalf("count mismatch: %v", times)
	}
	if times[0] != 2.2 || times[4] != 19.8 {
		t.Fatalf("time mismatch: %v", times)
	}
	if times := coverageTimes(1, 12); len(times) != 1 || times[0] != 0.5 {
		t.Fatalf("short duration mismatch: %v", times)
	}
}

func TestOutputRecoveryReason(t *testing.T) {
	frames := []Frame{{Status: StatusDone}}
	if value := OutputRecoveryReason(Task{Frames: frames, Output: "/x", OutputHash: "a"}, "b", true, false); value != "changed" {
		t.Fatalf("changed mismatch: %s", value)
	}
	if value := OutputRecoveryReason(Task{Frames: frames, Cleaned: false}, "", false, false); value != "legacy" {
		t.Fatalf("legacy mismatch: %s", value)
	}
	if value := OutputRecoveryReason(Task{Frames: frames, OutputPending: true}, "", false, false); value != "pending" {
		t.Fatalf("pending mismatch: %s", value)
	}
	if value := OutputRecoveryReason(Task{Frames: frames, OutputJournal: &Journal{}}, "", false, false); value != "journal" {
		t.Fatalf("journal mismatch: %s", value)
	}
}

func TestCommitAndRecoverJournal(t *testing.T) {
	root := t.TempDir()
	service := New(root, t.TempDir(), "", "darwin", true)
	id := strings.Repeat("a", 32)
	task := Task{
		ID: id, Source: "/tmp/source", SourceHash: strings.Repeat("b", 64), Duration: 12,
		Frames: []Frame{{Path: "/tmp/frame", Time: 5, Hash: strings.Repeat("c", 64), Status: StatusDone, Text: "文字"}},
	}
	if err := service.commitOutput(&task, outputText(&task)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(task.Output)
	if err != nil || !strings.Contains(string(body), "文字") || !strings.Contains(string(body), "0:05") {
		t.Fatalf("output mismatch: %q %v", body, err)
	}
	if task.OutputHash == "" {
		t.Fatal("output hash missing")
	}
	newText := "新成果"
	newHash := digestText(newText)
	temp := task.Output + ".pending-" + strings.Repeat("d", 32)
	if err := os.WriteFile(temp, []byte(newText), 0o644); err != nil {
		t.Fatal(err)
	}
	task.OutputJournal = &Journal{Output: task.Output, Temp: temp, Hash: newHash, PreviousHash: task.OutputHash}
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.RecoverOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.OutputJournal != nil || recovered.OutputHash != newHash {
		t.Fatalf("recovery mismatch: %+v", recovered)
	}
	body, _ = os.ReadFile(recovered.Output)
	if string(body) != newText {
		t.Fatalf("recovered output mismatch: %q", body)
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("journal temp should be removed: %v", err)
	}
}

func TestListReverse(t *testing.T) {
	root := t.TempDir()
	service := New(root, t.TempDir(), "", "darwin", true)
	for _, id := range []string{strings.Repeat("1", 32), strings.Repeat("2", 32)} {
		task := Task{ID: id, Frames: []Frame{}, Gaps: []any{}}
		if err := service.save(task); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := service.List()
	if err != nil || len(tasks) != 2 || tasks[0].ID != strings.Repeat("2", 32) {
		t.Fatalf("list mismatch: %+v %v", tasks, err)
	}
	encoded, _ := json.Marshal(tasks[0])
	if !strings.Contains(string(encoded), `"frames":[]`) {
		t.Fatalf("empty frames must serialize: %s", encoded)
	}
	if info, err := os.Stat(filepath.Join(root, strings.Repeat("1", 32)+".json")); err != nil || info.Size() == 0 {
		t.Fatalf("task file missing: %v", err)
	}
}
