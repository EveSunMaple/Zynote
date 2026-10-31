package visual

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recognitionFixture(t *testing.T) (*Service, Task) {
	t.Helper()
	root := t.TempDir()
	service := New(filepath.Join(root, "tasks"), filepath.Join(root, "frames"), "", "win32", false)
	source := filepath.Join(root, "source.mp4")
	frame := filepath.Join(root, "frame.jpg")
	for _, path := range []string{source, frame} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := fileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	task := Task{ID: strings.Repeat("c", 32), Source: source, SourceHash: hash, Connection: "fixture", Frames: []Frame{{Path: frame, Hash: hash, Status: StatusWaiting}}}
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	return service, task
}

func TestRecognitionUnknownDoesNotReplay(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider-error", true: "cancel-after-response"}[cancelled], func(t *testing.T) {
			service, task := recognitionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			_, err := service.Recognize(ctx, task.ID, task.Connection, func(context.Context, Frame) (string, error) {
				calls++
				stored, err := service.load(task.ID)
				if err != nil || stored.Frames[0].Status != StatusDispatched {
					t.Fatalf("missing dispatch before external call: %+v %v", stored, err)
				}
				if cancelled {
					cancel()
					return "response", nil
				}
				return "", errors.New("transport lost")
			}, nil)
			if err == nil {
				t.Fatal("unknown outcome accepted")
			}
			// A reconstructed service models backend restart with no in-memory guard.
			fresh := New(service.Root, service.FramesRoot, "", "win32", false)
			_, err = fresh.Recognize(context.Background(), task.ID, task.Connection, func(context.Context, Frame) (string, error) { calls++; return "replayed", nil }, nil)
			if err == nil || calls != 1 {
				t.Fatalf("unknown replay: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestRecognitionSavedResponseCanRecoverWithoutReplay(t *testing.T) {
	service, task := recognitionFixture(t)
	calls := 0
	invoke := func(context.Context, Frame) (string, error) { calls++; return "saved response", nil }
	result, err := service.Recognize(context.Background(), task.ID, task.Connection, invoke, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Frames[0].Status != StatusDone {
		t.Fatalf("not saved: %+v", result)
	}
	if _, err := service.Recognize(context.Background(), task.ID, task.Connection, invoke, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("saved response replayed")
	}
}
