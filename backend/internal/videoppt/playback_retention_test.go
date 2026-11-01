package videoppt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const retentionTestID = "9e326025-30b4-45d0-bd27-4a30275adccb"
const retentionReleaseID = "9e326025-30b4-45d0-bd27-4a30275adcc1"
const retentionSRT = "1\n00:00:00,000 --> 00:00:01,000\nSaved words\n"

func retentionSetup(t *testing.T, root string) (*Service, CreatedTask, RetentionEntry, string) {
	t.Helper()
	if root == "" {
		var err error
		root, err = filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
	}
	service := New(filepath.Join(root, "VideoTasks"))
	source := filepath.Join(root, "original.mp4")
	if err := os.WriteFile(source, []byte("managed media"), 0600); err != nil {
		t.Fatal(err)
	}
	hash := retentionHash([]byte("managed media"))
	task, err := service.CreateOperationTask(retentionTestID, "fixture", VideoOperation{Version: 1, Stage: "subtitle", Path: source, SourceHash: hash, OutputName: "fixture.srt", Page: 1, RetainPlayback: true})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.ReservePlaybackRetention(context.Background(), task.ID, 13)
	if err != nil {
		t.Fatal(err)
	}
	if err := CopyVideoReady(context.Background(), source, task.Path); err != nil {
		t.Fatal(err)
	}
	return service, task, entry, source
}
func retentionPublish(t *testing.T, service *Service, task CreatedTask) (string, *PlaybackBinding) {
	t.Helper()
	ctx := context.Background()
	if err := SavePreparedSubtitle(task, retentionSRT, 1); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePlaybackRetention(ctx, task.ID, retentionSRT); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(filepath.Dir(service.Root), "Transcripts")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := PublishSubtitle(ctx, task, directory, "fixture.srt", []byte(retentionSRT))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSubtitleSaved(task.ID, path); err != nil {
		t.Fatal(err)
	}
	binding, err := service.CompletePlaybackRetention(ctx, task.ID, path)
	if err != nil {
		t.Fatal(err)
	}
	return path, binding
}
func releaseInput(entry RetentionEntry) PlaybackReleaseRequest {
	return PlaybackReleaseRequest{ID: entry.ID, ExpectedRefKind: "intent", ExpectedRefSHA256: entry.IntentSHA256, ReleaseID: retentionReleaseID}
}
func finishRelease(t *testing.T, service *Service, input PlaybackReleaseRequest) PlaybackReleaseResult {
	t.Helper()
	ctx := context.Background()
	prepared, err := service.PreparePlaybackRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.ReleaseID = prepared.Plan.ReleaseID
	input.ExpectedTombstoneSHA256 = prepared.TombstoneSHA256
	result, err := service.FinalizePlaybackRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestRetentionDefaultFalseIdentityAndUnsupportedSources(t *testing.T) {
	body, _ := json.Marshal(VideoOperation{Version: 1, Stage: "subtitle"})
	if strings.Contains(string(body), "retainPlayback") {
		t.Fatal("default false changed old serialized identity")
	}
	service, task, _, _ := retentionSetup(t, "")
	operation, _ := service.Operation(task.ID)
	operation.SourceHash = "bad"
	if err := saveTaskJSON(filepath.Join(task.Dir, "operation.json"), operation); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(service.Root, ".retained", "index.json"))
	if _, err := service.ReservePlaybackRetention(context.Background(), task.ID, 13); err == nil {
		t.Fatal("invalid source hash admitted")
	}
	after, _ := os.ReadFile(filepath.Join(service.Root, ".retained", "index.json"))
	if string(before) != string(after) {
		t.Fatal("tamper polluted index")
	}
	operation.RetainPlayback = true
	operation.Stage = "auto"
	if _, err := service.CreateOperationTask("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bad", operation); err == nil {
		t.Fatal("nonlocal/subtitle retention admitted")
	}
}
func TestRetentionCandidateTamperAndSavedOnlyNeverBind(t *testing.T) {
	for _, kind := range []string{"missing", "source", "operation", "srt"} {
		t.Run(kind, func(t *testing.T) {
			service, task, entry, _ := retentionSetup(t, "")
			ctx := context.Background()
			if err := service.PreparePlaybackRetention(ctx, task.ID, retentionSRT); err != nil {
				t.Fatal(err)
			}
			candidatePath := filepath.Join(task.Dir, "retention-candidate.json")
			if kind == "missing" {
				if err := os.Remove(candidatePath); err != nil {
					t.Fatal(err)
				}
			} else {
				var candidate retentionCandidate
				body, _ := os.ReadFile(candidatePath)
				_ = json.Unmarshal(body, &candidate)
				switch kind {
				case "source":
					candidate.SourceSHA256 = strings.Repeat("a", 64)
				case "operation":
					candidate.OperationSHA256 = strings.Repeat("a", 64)
				case "srt":
					candidate.SubtitleSHA256 = retentionHash([]byte(strings.ReplaceAll(retentionSRT, "Saved", "Changed")))
				}
				if err := saveTaskJSON(candidatePath, candidate); err != nil {
					t.Fatal(err)
				}
			}
			directory := filepath.Join(filepath.Dir(service.Root), "Transcripts")
			_ = os.MkdirAll(directory, 0700)
			saved := filepath.Join(directory, "fixture.srt")
			_ = os.WriteFile(saved, []byte(retentionSRT), 0600)
			if _, err := service.CompletePlaybackRetention(ctx, task.ID, saved); err == nil {
				t.Fatal("candidate tamper silently bound")
			}
			if _, err := os.Lstat(filepath.Join(task.Dir, "retention-receipt.json")); !os.IsNotExist(err) {
				t.Fatal("new receipt on conflict")
			}
			rows, err := service.RetainedPlayback(ctx, 16)
			if err != nil || len(rows) != 1 || rows[0].IntentSHA256 != entry.IntentSHA256 {
				t.Fatal(rows, err)
			}
		})
	}
}
func TestRetentionRetainedCompletionPendingAndReleasePreservesOutputs(t *testing.T) {
	service, task, entry, source := retentionSetup(t, "")
	saved, binding := retentionPublish(t, service, task)
	if _, err := service.PlaybackSource(context.Background(), task.ID, entry.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteOperation(task, map[string]any{"playbackBinding": binding}, saved); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	beforeSource, _ := os.ReadFile(source)
	beforeSaved, _ := os.ReadFile(saved)
	input := releaseInput(entry)
	input.ExpectedRefKind = "receipt"
	input.ExpectedRefSHA256 = binding.RetentionReceiptSHA256
	result := finishRelease(t, service, input)
	if result.Plan.State != "released" {
		t.Fatal(result)
	}
	afterSource, _ := os.ReadFile(source)
	afterSaved, _ := os.ReadFile(saved)
	if string(beforeSource) != string(afterSource) || string(beforeSaved) != string(afterSaved) {
		t.Fatal("release changed original or saved SRT")
	}
	if _, err := service.PlaybackSource(context.Background(), task.ID, entry.SourceSHA256); err == nil {
		t.Fatal("released playback reopened")
	}
	if _, err := service.RunVideoTask(context.Background(), task.ID, func(context.Context) (any, error) { t.Fatal("released task executed"); return nil, nil }); err == nil {
		t.Fatal("tombstone ignored")
	}
	rows, err := service.RetainedPlayback(context.Background(), 16)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	again := finishRelease(t, service, input)
	if again.Plan.ReleaseID != result.Plan.ReleaseID {
		t.Fatal("restart changed release plan")
	}
}
func TestRetentionFailedIntentMissingEmptyPartialRemainReachable(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "partial"} {
		t.Run(kind, func(t *testing.T) {
			service, task, entry, source := retentionSetup(t, "")
			path := filepath.Join(task.Dir, "retention-intent.json")
			switch kind {
			case "missing":
				_ = os.Remove(path)
			case "empty":
				_ = os.WriteFile(path, nil, 0600)
			case "partial":
				_ = os.WriteFile(path, []byte(`{"version":`), 0600)
			}
			// Failed copy can leave a shorter managed video. The exact intent, not an
			// invented complete media digest, authorizes the explicit release plan.
			_ = os.WriteFile(task.Path, []byte("partial"), 0600)
			rows, err := service.RetainedPlayback(context.Background(), 16)
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			result := finishRelease(t, New(service.Root), releaseInput(entry))
			if result.Plan.State != "released" {
				t.Fatal(result)
			}
			original, _ := os.ReadFile(source)
			if string(original) != "managed media" {
				t.Fatal("original deleted")
			}
		})
	}
}
func TestRetentionQuotaIncludesFailedIntentAndBytes(t *testing.T) {
	service, task, entry, _ := retentionSetup(t, "")
	_ = os.Remove(filepath.Join(task.Dir, "retention-intent.json"))
	operation, _ := service.Operation(task.ID)
	// Four reserved 8GiB commitments consume the 32GiB budget without allocating media.
	for n := 1; n < 5; n++ {
		id := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", n)
		_, err := service.CreateOperationTask(id, "fixture", operation)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.ReservePlaybackRetention(context.Background(), id, 8*1024*1024*1024)
		if n == 4 {
			if err == nil {
				t.Fatal("byte cap bypass")
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := service.RetainedPlayback(context.Background(), 16)
	if len(rows) != 4 || rows[0].ID == "" || entry.IntentSHA256 == "" {
		t.Fatal(rows)
	}
	// Item cap is independent of video byte cap.
	other, _, _, _ := retentionSetup(t, "")
	for n := 1; n <= 16; n++ {
		id := fmt.Sprintf("bbbbbbbb-bbbb-4bbb-8bbb-%012d", n)
		_, err := other.CreateOperationTask(id, "fixture", operation)
		if err != nil {
			t.Fatal(err)
		}
		_, err = other.ReservePlaybackRetention(context.Background(), id, 13)
		if n == 16 {
			if err == nil {
				t.Fatal("item cap bypass")
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
func TestRetentionReleaseLeaseCASPartialDeletionAndUnknownFiles(t *testing.T) {
	service, task, entry, _ := retentionSetup(t, "")
	ctx := context.Background()
	input := releaseInput(entry)
	active := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := service.RunVideoTask(ctx, task.ID, func(context.Context) (any, error) { close(active); <-stop; return nil, nil })
		done <- err
	}()
	<-active
	if _, err := service.PreparePlaybackRelease(ctx, input); err == nil {
		t.Fatal("active callback released")
	} else {
		var app *contract.AppError
		if !errors.As(err, &app) || app.Code != contract.CodeBusy {
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(task.Dir, "unknown.txt")
	_ = os.WriteFile(unknown, []byte("keep"), 0600)
	if _, err := service.PreparePlaybackRelease(ctx, input); err == nil {
		t.Fatal("unknown file admitted")
	}
	_ = os.Remove(unknown)
	prepared, err := service.PreparePlaybackRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	retry := input
	retry.ReleaseID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	same, err := New(service.Root).PreparePlaybackRelease(ctx, retry)
	if err != nil || same.Plan.ReleaseID != input.ReleaseID || same.TombstoneSHA256 != prepared.TombstoneSHA256 {
		t.Fatal(same, err)
	}
	input.ExpectedTombstoneSHA256 = strings.Repeat("a", 64)
	if _, err := service.FinalizePlaybackRelease(ctx, input); err == nil {
		t.Fatal("tombstone CAS ignored")
	}
	if _, err := os.Stat(task.Path); err != nil {
		t.Fatal("bad CAS deleted media")
	}
	input.ExpectedTombstoneSHA256 = prepared.TombstoneSHA256
	_ = os.Remove(filepath.Join(task.Dir, prepared.Plan.Files[0].Name))
	if _, err := service.FinalizePlaybackRelease(ctx, input); err != nil {
		t.Fatal("partial deletion could not resume", err)
	}
}
func TestRetentionReleaseFilenameBudgetsAndAliases(t *testing.T) {
	t.Run("large-valid-prepared", func(t *testing.T) {
		service, task, entry, _ := retentionSetup(t, "")
		srt := strings.ReplaceAll(retentionSRT, "Saved words", strings.Repeat("valid words ", 8000))
		if err := SavePreparedSubtitle(task, srt, 1); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(filepath.Join(task.Dir, "subtitle-prepared.json"))
		if info.Size() <= 65536 {
			t.Fatal("not large")
		}
		finishRelease(t, service, releaseInput(entry))
	})
	for _, name := range []string{"operation.json", "input-subtitle-" + strings.Repeat("a", 64) + ".json", "subtitle-export-" + strings.Repeat("a", 64) + ".json", "subtitle-prepared.json"} {
		t.Run(name, func(t *testing.T) {
			service, task, entry, _ := retentionSetup(t, "")
			file, err := os.Create(filepath.Join(task.Dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(releaseFileMaximum(name) + 1); err != nil {
				t.Fatal(err)
			}
			_ = file.Close()
			if _, err := service.PreparePlaybackRelease(context.Background(), releaseInput(entry)); err == nil {
				t.Fatal("oversize admitted")
			}
			if _, err := os.Stat(task.Path); err != nil {
				t.Fatal("preflight deleted media")
			}
		})
	}
	t.Run("alias", func(t *testing.T) {
		service, task, entry, source := retentionSetup(t, "")
		_ = os.Remove(filepath.Join(task.Dir, "video-ready.json"))
		if err := os.Symlink(source, filepath.Join(task.Dir, "video-ready.json")); err != nil {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		if _, err := service.PreparePlaybackRelease(context.Background(), releaseInput(entry)); err == nil {
			t.Fatal("alias admitted")
		}
	})
}

func TestRetentionCrashWorker(t *testing.T) {
	if os.Getenv("ZYNOTE_RETENTION_CRASH_WORKER") != "1" {
		return
	}
	root := os.Getenv("ZYNOTE_RETENTION_CRASH_ROOT")
	boundary := os.Getenv("ZYNOTE_RETENTION_CRASH_BOUNDARY")
	retentionBoundary = func(name string) {
		if name == boundary {
			if err := os.WriteFile(filepath.Join(root, "reached"), []byte(name), 0600); err != nil {
				panic(err)
			}
			select {}
		}
	}
	service, task, entry, _ := retentionSetup(t, root)
	if boundary == "reservation-index" || boundary == "intent-written" {
		t.Fatal("boundary not reached")
	}
	saved, _ := retentionPublish(t, service, task)
	if strings.HasPrefix(boundary, "release-") {
		_ = saved
		finishRelease(t, service, releaseInput(entry))
	}
	t.Fatal("boundary not reached")
}
func TestRetentionRealProcessKillRecovery(t *testing.T) {
	boundaries := []string{"reservation-index", "intent-written", "candidate-written", "candidate-index", "receipt-written", "retention-cleanup-one", "retained-index", "release-staging-written", "release-plan", "release-delete-one", "release-released", "release-index-removed"}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestRetentionCrashWorker$", "-test.timeout=60s")
			command.Env = append(os.Environ(), "ZYNOTE_RETENTION_CRASH_WORKER=1", "ZYNOTE_RETENTION_CRASH_ROOT="+root, "ZYNOTE_RETENTION_CRASH_BOUNDARY="+boundary)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- command.Wait() }()
			deadline := time.NewTimer(15 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			reached := false
			for !reached {
				select {
				case err := <-exited:
					t.Fatalf("worker exited before kill: %v", err)
				case <-deadline.C:
					_ = command.Process.Kill()
					<-exited
					t.Fatal("boundary timeout")
				case <-ticker.C:
					_, err := os.Stat(filepath.Join(root, "reached"))
					reached = err == nil
				}
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := <-exited; err == nil {
				t.Fatal("process was not killed")
			}
			service := New(filepath.Join(root, "VideoTasks"))
			rows, err := service.RetainedPlayback(context.Background(), 16)
			if err != nil {
				t.Fatal(err)
			}
			if boundary != "release-index-removed" && len(rows) != 1 {
				t.Fatal("lost commitment after kill", rows)
			}
			var entry RetentionEntry
			if len(rows) > 0 {
				entry = rows[0]
			} else {
				var plan PlaybackReleasePlan
				_, err := retentionRecord(context.Background(), filepath.Join(service.Root, retentionTestID, "playback-release.json"), false, &plan)
				if err != nil {
					t.Fatal(err)
				}
				entry = RetentionEntry{ID: plan.ID, IntentSHA256: plan.RefSHA256}
			}

			// Reconstruct the actual durable retention transaction without an
			// ASR/model dependency before exercising explicit release.
			if boundary == "candidate-written" || boundary == "candidate-index" || boundary == "receipt-written" || boundary == "retention-cleanup-one" || boundary == "retained-index" {
				task := CreatedTask{ID: retentionTestID, Name: "fixture", Dir: filepath.Join(service.Root, retentionTestID), Path: filepath.Join(service.Root, retentionTestID, videoFileName)}
				saved := filepath.Join(root, "Transcripts", "fixture.srt")
				if strings.HasPrefix(boundary, "candidate-") {
					prepared, found, err := LoadPreparedSubtitle(task)
					if err != nil || !found || prepared.SRT != retentionSRT {
						t.Fatal("lost prepared model output", err)
					}
					if err := service.PreparePlaybackRetention(context.Background(), task.ID, prepared.SRT); err != nil {
						t.Fatal(err)
					}
					_ = os.MkdirAll(filepath.Dir(saved), 0700)
					published, err := PublishSubtitle(context.Background(), task, filepath.Dir(saved), "fixture.srt", []byte(prepared.SRT))
					if err != nil || published != saved {
						t.Fatal("publication recovery", err)
					}
					if err := service.MarkSubtitleSaved(task.ID, saved); err != nil {
						t.Fatal(err)
					}
				}
				binding, err := service.CompletePlaybackRetention(context.Background(), task.ID, saved)
				if err != nil {
					t.Fatal("retention recovery without ASR", err)
				}
				if err := service.CompleteOperation(task, map[string]any{"playbackBinding": binding}, saved); err != nil {
					t.Fatal(err)
				}
			}
			finishRelease(t, service, releaseInput(entry))
			original, err := os.ReadFile(filepath.Join(root, "original.mp4"))
			if err != nil || string(original) != "managed media" {
				t.Fatal("original affected", err)
			}
			if boundary != "reservation-index" && boundary != "intent-written" && boundary != "candidate-written" && boundary != "candidate-index" {
				body, err := os.ReadFile(filepath.Join(root, "Transcripts", "fixture.srt"))
				if err != nil || string(body) != retentionSRT {
					t.Fatal("saved output affected", err)
				}
			}
		})
	}
}

func TestRetentionMutexWaitHonorsContext(t *testing.T) {
	if err := retentionMu.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer retentionMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := retentionMu.Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("retention wait ignored context", err)
	}
}
func TestRetentionWindowsCaseVariantLifecycle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path case semantics")
	}
	service, task, _, _ := retentionSetup(t, "")
	variant := New(strings.ToUpper(service.Root))
	entry, err := variant.ReservePlaybackRetention(context.Background(), task.ID, 13)
	if err != nil {
		t.Fatal(err)
	}
	_, binding := retentionPublish(t, variant, task)
	input := releaseInput(entry)
	input.ExpectedRefKind = "receipt"
	input.ExpectedRefSHA256 = binding.RetentionReceiptSHA256
	finishRelease(t, variant, input)
}

func TestRetentionRejectsDirectoryAlias(t *testing.T) {
	service, task, _, source := retentionSetup(t, "")
	alias := filepath.Join(filepath.Dir(service.Root), "VideoTasks-alias")
	if runtime.GOOS == "windows" {
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", alias, service.Root).CombinedOutput(); err != nil {
			t.Fatalf("junction fixture: %v %s", err, output)
		}
	} else {
		if err := os.Symlink(service.Root, alias); err != nil {
			t.Fatal(err)
		}
	}
	defer os.Remove(alias)
	if _, err := New(alias).ReservePlaybackRetention(context.Background(), task.ID, 13); err == nil {
		t.Fatal("directory alias admitted")
	}
	original, err := os.ReadFile(source)
	if err != nil || string(original) != "managed media" {
		t.Fatal("alias test affected source", err)
	}
}

func TestRetentionPendingProjectionDoesNotWaitForLedgerWriter(t *testing.T) {
	service, task, _, _ := retentionSetup(t, "")
	saved, binding := retentionPublish(t, service, task)
	if err := service.CompleteOperation(task, map[string]any{"playbackBinding": binding}, saved); err != nil {
		t.Fatal(err)
	}
	if err := retentionMu.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer retentionMu.Unlock()
	done := make(chan error, 1)
	go func() {
		rows, err := service.Pending()
		if err == nil && len(rows) != 0 {
			err = fmt.Errorf("retained task shown pending: %d", len(rows))
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Pending waited behind ledger hash")
	}
}
