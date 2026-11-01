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
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

const apiRetentionID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
const apiRetentionSRT = "1\n00:00:00,000 --> 00:00:01,000\nSaved words\n"

func retainedAPIFixture(t *testing.T, retain bool) (*Invoker, *contract.State, videoppt.CreatedTask, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "original.mp4")
	if err := os.WriteFile(source, []byte("managed media"), 0600); err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	inv := NewInvoker()
	task, _, err := inv.createQueuedVideoTaskRetained(state, source, "", "subtitle", "", "", retain, apiRetentionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := videoppt.CopyVideoReady(context.Background(), source, task.Path); err != nil {
		t.Fatal(err)
	}
	return inv, state, task, source
}
func TestRetentionAPIExplicitFlagFreezesBeforeQueueAndDefaultIsUnchanged(t *testing.T) {
	for _, flag := range []string{"", `,"retainPlayback":true`} {
		t.Run(flag, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "original.mp4")
			_ = os.WriteFile(source, []byte("managed media"), 0600)
			store := &subtitleStore{state: &contract.State{LibraryRoot: root}, picker: &subtitlePicker{path: &source}}
			inv := NewInvoker()
			holdQueueWorker(t, inv)
			params := json.RawMessage(`{"operationId":"` + apiRetentionID + `"` + flag + `}`)
			done := submitSubtitle(inv, store, "video-ppt:subtitle-transcribe-local", params)
			waitVideoQueued(t, inv, store.state, done)
			cancelQueuedCall(t, inv, done)
			operation, err := videoPptService(store.state).Operation(apiRetentionID)
			if err != nil || operation.RetainPlayback != (flag != "") {
				t.Fatal(operation, err)
			}
			rows, err := videoPptService(store.state).RetainedPlayback(context.Background(), 16)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if flag != "" {
				want = 1
			}
			if len(rows) != want {
				t.Fatal("pre-ASR quota missing", rows)
			}
			if _, _, err := inv.existingLocalSubtitle(store.state, apiRetentionID, "", flag == ""); err == nil {
				t.Fatal("immutable flag changed")
			}
			if store.picker.calls != 1 {
				t.Fatal("picker repeated")
			}
		})
	}
}
func TestRetentionAPIPreparedRecoveryCompletesOnceWithoutModelsAndDefaultDeletesCopy(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "retained"}[retain], func(t *testing.T) {
			_, state, task, source := retainedAPIFixture(t, retain)
			if err := videoppt.SavePreparedSubtitle(task, apiRetentionSRT, 1); err != nil {
				t.Fatal(err)
			}
			// No native/model service is configured. Any unexpected ASR attempt fails.
			params, _ := json.Marshal(map[string]string{"id": task.ID})
			value, err := NewInvoker().handleVideoResumeSubtitleContext(context.Background(), nil, state, params)
			if err != nil {
				t.Fatal(err)
			}
			output := value.(map[string]any)
			if (output["playbackBinding"] != nil) != retain {
				t.Fatal("wrong binding", output)
			}
			_, statErr := os.Stat(task.Path)
			if retain && statErr != nil {
				t.Fatal("retained copy removed", statErr)
			}
			if !retain && !os.IsNotExist(statErr) {
				t.Fatal("default cleanup changed", statErr)
			}
			operation, _ := videoPptService(state).Operation(task.ID)
			_, err = videoPptService(state).CreateOperationTask(task.ID, "", operation)
			completed, ok := err.(*videoppt.CompletedOperationError)
			if !ok || completed.Path != output["savedPath"] {
				t.Fatal("completion not reusable", err)
			}
			before, _ := os.ReadFile(output["savedPath"].(string))
			original, _ := os.ReadFile(source)
			if string(before) != apiRetentionSRT || string(original) != "managed media" {
				t.Fatal("source/output changed")
			}
			pending, err := videoPptService(state).Pending()
			if err != nil || len(pending) != 0 {
				t.Fatal(pending, err)
			}
		})
	}
}
func TestRetentionAPISavedOnlyPreservesMediaWithoutInventingCandidate(t *testing.T) {
	_, state, task, _ := retainedAPIFixture(t, true)
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", "")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := videoppt.PublishSubtitle(context.Background(), task, directory, "fixture.srt", []byte(apiRetentionSRT))
	if err != nil {
		t.Fatal(err)
	}
	if err := videoPptService(state).MarkSubtitleSaved(task.ID, saved); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	value, err := NewInvoker().handleVideoResumeSubtitleContext(context.Background(), nil, state, params)
	if err != nil {
		t.Fatal(err)
	}
	output := value.(map[string]any)
	if output["playbackBinding"] != nil || output["playbackUnavailable"] != true || output["savedPath"] != saved {
		t.Fatal(output)
	}
	if _, err := os.Stat(task.Path); err != nil {
		t.Fatal("saved-only removed managed copy")
	}
	for _, name := range []string{"retention-candidate.json", "retention-receipt.json"} {
		if _, err := os.Stat(filepath.Join(task.Dir, name)); !os.IsNotExist(err) {
			t.Fatal("fabricated historical binding", name)
		}
	}
}
func TestRetentionAPICandidateMissingPreparedCannotRepeatASR(t *testing.T) {
	inv, state, task, _ := retainedAPIFixture(t, true)
	if err := videoPptService(state).PreparePlaybackRetention(context.Background(), task.ID, apiRetentionSRT); err != nil {
		t.Fatal(err)
	}
	if _, _, err := inv.preparedVideoSubtitle(context.Background(), state, task); err == nil {
		t.Fatal("candidate missing prepared allowed ASR")
	}
	// The stable diagnosis must come from the saved candidate guard, rather than
	// the missing native service, so this asserts the actual no-re-ASR seam.
	_, _, err := inv.preparedVideoSubtitle(context.Background(), state, task)
	if err == nil || err.Error() != "UNAVAILABLE: 留存候选已存在但转写检查点缺失，请保留任务，未重复转写" {
		t.Fatal(err)
	}
}
func TestRetentionAPIQueuedCallbackRechecksTombstoneBeforeDispatch(t *testing.T) {
	inv, state, task, _ := retainedAPIFixture(t, true)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = inv.jobs.Run("held", "", func(context.Context) (any, error) { close(entered); <-release; return nil, nil })
	}()
	<-entered
	done := make(chan error, 1)
	called := false
	go func() {
		_, err := inv.runSubtitleQueuedContext(context.Background(), state, task.ID, func(context.Context) (any, error) { called = true; return nil, nil })
		done <- err
	}()
	waitVideoQueued(t, inv, state, done)
	rows, err := videoPptService(state).RetainedPlayback(context.Background(), 16)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	input := videoppt.PlaybackReleaseRequest{ID: task.ID, ExpectedRefKind: "intent", ExpectedRefSHA256: rows[0].IntentSHA256, ReleaseID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd"}
	prepared, err := videoPptService(state).PreparePlaybackRelease(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedTombstoneSHA256 = prepared.TombstoneSHA256
	if _, err := videoPptService(state).FinalizePlaybackRelease(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-finished
	select {
	case err := <-done:
		if err == nil || called {
			t.Fatal("queued callback ignored tombstone", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queue failed to settle")
	}
}

func TestRetentionAPIListStrictOptionalLimit(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	inv := NewInvoker()
	for _, params := range []string{`null`, `{"limit":0}`, `{"limit":-1}`, `{"limit":17}`, `{"limit":null}`, `{"limit":"1"}`, `{"unknown":1}`} {
		if _, err := inv.handleRetainedPlayback(nil, state, json.RawMessage(params)); err == nil {
			t.Fatal("invalid list input accepted", params)
		} else {
			var app *contract.AppError
			if !errors.As(err, &app) || app.Code != contract.CodeInputInvalid {
				t.Fatal(params, err)
			}
		}
	}
	for _, params := range []string{`{}`, `{"limit":1}`, `{"limit":16}`} {
		if _, err := inv.handleRetainedPlayback(nil, state, json.RawMessage(params)); err != nil {
			t.Fatal(params, err)
		}
	}
}

func TestRetentionAPIPrepareNoMutationOnlyForConfirmedPrewriteFailure(t *testing.T) {
	inv, state, task, _ := retainedAPIFixture(t, true)
	service := videoPptService(state)
	rows, err := service.RetainedPlayback(context.Background(), 16)
	if err != nil {
		t.Fatal(err)
	}
	input := videoppt.PlaybackReleaseRequest{ID: task.ID, ExpectedRefKind: "intent", ExpectedRefSHA256: rows[0].IntentSHA256, ReleaseID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd"}
	entered := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := service.RunVideoTask(context.Background(), task.ID, func(context.Context) (any, error) { close(entered); <-stop; return nil, nil })
		done <- err
	}()
	<-entered
	params, _ := json.Marshal(input)
	value, err := inv.handlePreparePlaybackRelease(nil, state, params)
	close(stop)
	if taskErr := <-done; taskErr != nil {
		t.Fatal(taskErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	rejected, ok := value.(map[string]any)
	if !ok || rejected["status"] != "rejected" || rejected["mutation"] != "none" || rejected["code"] != "BUSY" {
		t.Fatal(value)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, "playback-release.json")); !os.IsNotExist(err) {
		t.Fatal("BUSY wrote tombstone")
	}
	if _, err := service.PreparePlaybackRelease(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRefSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	params, _ = json.Marshal(input)
	value, err = inv.handlePreparePlaybackRelease(nil, state, params)
	if err == nil || value != nil {
		t.Fatal("existing tombstone conflict falsely declared no mutation", value, err)
	}
}

func TestRetentionAPIPartialStagingCannotClaimNoMutation(t *testing.T) {
	inv, state, task, _ := retainedAPIFixture(t, true)
	service := videoPptService(state)
	rows, err := service.RetainedPlayback(context.Background(), 16)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(task.Dir, ".playback-release-preparing.json")
	if err := os.WriteFile(staging, []byte(`{"version":`), 0600); err != nil {
		t.Fatal(err)
	}
	input := videoppt.PlaybackReleaseRequest{ID: task.ID, ExpectedRefKind: "intent", ExpectedRefSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ReleaseID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd"}
	params, _ := json.Marshal(input)
	value, err := inv.handlePreparePlaybackRelease(nil, state, params)
	if err == nil || value != nil {
		t.Fatal("partial durable staging falsely declared no mutation", value, err)
	}
	if body, err := os.ReadFile(staging); err != nil || string(body) != `{"version":` {
		t.Fatal("rejected request changed staging", err)
	}
	input.ExpectedRefSHA256 = rows[0].IntentSHA256
	prepared, err := service.PreparePlaybackRelease(context.Background(), input)
	if err != nil {
		t.Fatal("exact reference could not reconcile partial staging", err)
	}
	input.ExpectedTombstoneSHA256 = prepared.TombstoneSHA256
	if _, err := service.FinalizePlaybackRelease(context.Background(), input); err != nil {
		t.Fatal(err)
	}
}
