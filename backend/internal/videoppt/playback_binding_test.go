package videoppt

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func TestBoundRetentionExactChainAndReadOnlyFailures(t *testing.T) {
	for _, mutation := range []string{"subtitle", "receipt", "candidate", "intent", "operation", "release", "course", "saved-locator"} {
		t.Run(mutation, func(t *testing.T) {
			service, task, _, _ := retentionSetup(t, "")
			path, binding := retentionPublish(t, service, task)
			verified, err := service.RetainedSubtitle(context.Background(), task.ID, binding.RetentionReceiptSHA256, "")
			if err != nil {
				t.Fatal(err)
			}
			expected, srt, _, err := verified.ForCreation(context.Background(), filepath.Dir(service.Root), "")
			if err != nil || srt != retentionSRT || verified.Declaration().SourceSHA256 != binding.SourceSHA256 {
				t.Fatal(expected, err)
			}
			course := ""
			switch mutation {
			case "subtitle":
				_ = os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:00:01,000\nOther words\n"), 0600)
			case "receipt", "candidate", "intent":
				_ = os.WriteFile(filepath.Join(task.Dir, "retention-"+mutation+".json"), []byte("{}"), 0600)
			case "operation":
				_ = os.WriteFile(filepath.Join(task.Dir, "operation.json"), []byte("{}"), 0600)
			case "release":
				_ = os.WriteFile(filepath.Join(task.Dir, ".playback-release-preparing.json"), []byte("{}"), 0600)
			case "course":
				course = "another"
			case "saved-locator":
				expected.SavedSubtitle = "Transcripts/other.srt"
			}
			before := boundLibrarySnapshot(t, filepath.Dir(service.Root))
			if _, err := service.ValidateRetainedPlaybackBinding(context.Background(), expected, course); err == nil {
				t.Fatal("changed chain accepted")
			}
			after := boundLibrarySnapshot(t, filepath.Dir(service.Root))
			if before != after {
				t.Fatal("read-only verifier mutated library")
			}
		})
	}
}

func boundLibrarySnapshot(t *testing.T, root string) string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		files[relative] = retentionHash(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(files)
	return string(body)
}

func TestBoundRetentionCancelledAndZeroDescriptor(t *testing.T) {
	service, task, _, _ := retentionSetup(t, "")
	_, binding := retentionPublish(t, service, task)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.RetainedSubtitle(ctx, task.ID, binding.RetentionReceiptSHA256, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, _, err := (VerifiedRetainedSubtitle{}).ForCreation(context.Background(), filepath.Dir(service.Root), ""); err == nil {
		t.Fatal("zero descriptor accepted")
	}
	if ValidPlaybackBinding(contract.PlaybackSourceBinding{Version: 1, SavedSubtitle: "Transcripts/../secret"}) {
		t.Fatal("invalid binding accepted")
	}
}
