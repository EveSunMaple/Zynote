package subtitlederived

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectSnapshotBindingAndUnknownNeverReplay(t *testing.T) {
	root := t.TempDir()
	service := New(filepath.Join(root, "NoteGeneration"), root)
	srt := "1\n00:00:00,000 --> 00:00:02,000\nHello\n"
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	task, err := service.CreateDirectOperation(srt, "Lecture", "brief", "", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Items[0].Source.URL != "" || task.Items[0].Source.Kind != "direct-srt" {
		t.Fatal("fabricated remote identity")
	}
	if _, err := service.CreateDirectOperation(srt, "Changed title", "brief", "", id); err == nil {
		t.Fatal("changed title accepted")
	}
	calls := 0
	deps := Dependencies{ResolveRecipes: fixtureRecipes, Generation: func(context.Context, string) (string, error) {
		t.Fatal("direct SRT queried remote generation")
		return "", nil
	}, Summarize: func(context.Context, string, string) (string, []Point, error) {
		calls++
		return "", nil, context.DeadlineExceeded
	}}
	for range 2 {
		result, err := service.Run(context.Background(), id, deps)
		if err != nil || result.Items[0].Status != "失败" {
			t.Fatalf("unexpected result %+v %v", result, err)
		}
	}
	if calls != 1 {
		t.Fatalf("unknown provider call replayed: %d", calls)
	}
	if err := os.WriteFile(task.Items[0].Source.Path, []byte(srt+"edit"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), id, deps)
	if err != nil || result.Items[0].Status != "失败" || calls != 1 {
		t.Fatal("changed snapshot dispatched")
	}
}

func TestDerivedPublicationPreservesPartialFinalAndIgnoresAbandonedTemporary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "result.md")
	if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	created := []string{}
	if _, err := writeDerivedFiles([]derivedFile{{Path: path, Text: "complete"}}, &created); err == nil {
		t.Fatal("partial final overwritten")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "partial" {
		t.Fatal("conflict changed")
	}
	next := filepath.Join(root, "next.md")
	if err := os.WriteFile(filepath.Join(root, ".note-abandoned"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeDerivedFiles([]derivedFile{{Path: next, Text: "complete"}}, &created); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(next)
	if string(body) != "complete" {
		t.Fatal("incomplete publication")
	}
}
