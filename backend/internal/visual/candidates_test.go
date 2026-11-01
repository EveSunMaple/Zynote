package visual

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/framecandidate"
)

func TestVisualProducerPersistsSelectionAndRejectsChangedSampler(t *testing.T) {
	service, _ := controlledMediaService(t, "producer")
	source := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(source, []byte("controlled video"), 0600); err != nil {
		t.Fatal(err)
	}
	task, err := service.Create(context.Background(), source, 4, "balanced", "fixture")
	if err != nil || task.CandidateEvidence == nil {
		t.Fatalf("missing producer evidence %+v %v", task, err)
	}
	manifest, err := framecandidate.Read(service.Root, task.ID, *task.CandidateEvidence, visualCandidateBinding(task.SourceHash, task.Source, 4, "balanced", 20))
	if err != nil || manifest.Stage != "selected" || len(manifest.Samples) != 4 {
		t.Fatalf("wrong selection %+v %v", manifest, err)
	}
	for index, sample := range manifest.Samples {
		if sample.TimeMs != int64(index*5000) || sample.Reason != "balanced-change" || sample.Metrics == nil {
			t.Fatalf("default sampling changed %+v", manifest)
		}
	}
	if _, err := service.Prepare(context.Background(), task.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	task.Preparation.Policy = "coverage"
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Prepare(context.Background(), task.ID, "fixture"); err == nil {
		t.Fatal("changed selection policy reused old evidence")
	}
	task.Preparation.Policy = "balanced"
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(service.Root, ".candidates", "*.json"))
	if len(files) != 1 {
		t.Fatalf("unexpected manifests %v", files)
	}
	file, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(" ")
	_ = file.Close()
	if _, err := service.Recognize(context.Background(), task.ID, "fixture", func(context.Context, Frame) (string, error) {
		t.Fatal("tampered selection dispatched provider")
		return "", nil
	}, nil); err == nil {
		t.Fatal("recognition accepted tampered selection evidence")
	}
}
