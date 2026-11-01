package videoppt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type interruptCopyReader struct {
	cancel context.CancelFunc
	err    error
	read   bool
}

func (r *interruptCopyReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, r.err
	}
	r.read = true
	if r.cancel != nil {
		r.cancel()
	}
	return copy(p, "partial"), nil
}

func TestStageVideoFailureNeverPublishesAndCanRetry(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "read-failure", true: "cancel-after-bytes"}[cancelled], func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "video.mp4")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("source interrupted")
			reader := &interruptCopyReader{err: failure}
			if cancelled {
				reader.cancel = cancel
				failure = context.Canceled
			}
			if err := stageVideo(ctx, reader, dest); !errors.Is(err, failure) {
				t.Fatalf("got %v, want %v", err, failure)
			}
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial published: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("staging leaked: %v %v", entries, err)
			}
			if err := stageVideo(context.Background(), bytes.NewBufferString("complete"), dest); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(dest)
			if string(data) != "complete" {
				t.Fatal("retry mismatch")
			}
		})
	}
}

type destinationRacingReader struct{ destination string }

func (r destinationRacingReader) Read(p []byte) (int, error) {
	if err := os.WriteFile(r.destination, []byte("winner"), 0600); err != nil {
		return 0, err
	}
	return copy(p, "loser"), io.EOF
}
func TestStageVideoConcurrentDestinationWins(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "video.mp4")
	if err := stageVideo(context.Background(), destinationRacingReader{dest}, dest); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected no-overwrite error, got %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "winner" {
		t.Fatal("destination overwritten")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatal("staging leaked")
	}
}
