package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func TestSubtitleQueueUserCancellationPreservesClassification(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel-one", true: "cancel-all"}[all], func(t *testing.T) {
			inv := NewInvoker()
			state := &contract.State{LibraryRoot: t.TempDir()}
			const id = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			entered := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := inv.runSubtitleQueuedContext(context.Background(), state, id, func(ctx context.Context) (any, error) {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				})
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("queue never dispatched")
			}
			cancelled := false
			if all {
				cancelled = inv.jobs.CancelAll()
			} else {
				cancelled = inv.jobs.Cancel("subtitle-transcribe:" + id)
			}
			if !cancelled {
				t.Fatal("user cancellation missed active task")
			}
			select {
			case err := <-done:
				var application *contract.AppError
				if !errors.As(err, &application) || application.Code != contract.CodeCancelled {
					t.Fatalf("user cancellation flattened %#v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("user cancellation stuck")
			}
		})
	}
}

func TestPreparedSubtitleAndTranscribePreserveContextClassification(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		for _, entry := range []string{"prepared", "transcribe", "resume-handler"} {
			t.Run(entry+map[bool]string{false: "-cancelled", true: "-timeout"}[timeout], func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				state := &contract.State{LibraryRoot: root, DataRoot: filepath.Join(root, "data")}
				source := filepath.Join(root, "source.mp4")
				original := []byte("opaque fixture: never decoded or sent to native")
				if err := os.WriteFile(source, original, 0600); err != nil {
					t.Fatal(err)
				}
				task, err := videoPptService(state).CreateSubtitleTask(source, "recording.srt", "", 1)
				if err != nil {
					t.Fatal(err)
				}
				var ctx context.Context
				var cancel context.CancelFunc
				expected := contract.CodeCancelled
				if timeout {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					expected = contract.CodeTimeout
				} else {
					ctx, cancel = context.WithCancel(context.Background())
					cancel()
				}
				defer cancel()
				inv := NewInvoker() // No ASR client, media resources, account, or provider.
				switch entry {
				case "prepared":
					_, _, err = inv.preparedVideoSubtitle(ctx, state, task)
				case "transcribe":
					_, err = inv.transcribeVideoTask(ctx, state, task)
				case "resume-handler":
					params, _ := json.Marshal(map[string]string{"id": task.ID})
					_, err = inv.handleVideoResumeSubtitleContext(ctx, nil, state, params)
				}
				var application *contract.AppError
				if !errors.As(err, &application) || application.Code != expected {
					t.Fatalf("%s context lost: %#v", entry, err)
				}
				body, readErr := os.ReadFile(source)
				if readErr != nil || string(body) != string(original) {
					t.Fatal("source changed", readErr)
				}
				if _, found, err := videoppt.LoadPreparedSubtitle(task); err != nil || found {
					t.Fatal("cancelled task published prepared result", found, err)
				}
				if _, err := os.Stat(state.DataRoot); !os.IsNotExist(err) {
					t.Fatal("cancelled task started ASR storage", err)
				}
			})
		}
	}
}
