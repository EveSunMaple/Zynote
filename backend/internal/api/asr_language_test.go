package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func TestASRLanguageRejectedBeforePickerOrTaskCreation(t *testing.T) {
	for _, action := range []string{"video-ppt:subtitle-transcribe-local", "video-ppt:subtitle-transcribe", "video-ppt:batch-subtitles"} {
		state := &contract.State{LibraryRoot: t.TempDir()}
		picker := &subtitlePicker{}
		store := &subtitleStore{state: state, picker: picker}
		_, err := NewInvoker().Invoke(store, action, json.RawMessage(`{"asrLanguage":" EN ","url":"https://www.bilibili.com/video/BV1xx411c7mD/","pages":[1]}`))
		if err == nil || picker.calls != 0 {
			t.Fatal(action, err, picker.calls)
		}
		if _, err := os.Stat(filepath.Join(state.LibraryRoot, "VideoTasks")); !os.IsNotExist(err) {
			t.Fatal("invalid request wrote task", action, err)
		}
	}
}

func TestLocalLanguageRetryUsesBindingAndChangedLanguageCannotRepick(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "lecture.mp4")
	if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &subtitleStore{state: &contract.State{LibraryRoot: root}, picker: &subtitlePicker{path: &source}}
	inv := NewInvoker()
	holdQueueWorker(t, inv)
	id := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	params := json.RawMessage(`{"operationId":"` + id + `","asrLanguage":"en"}`)
	for attempt := 0; attempt < 2; attempt++ {
		done := submitSubtitle(inv, store, "video-ppt:subtitle-transcribe-local", params)
		waitVideoQueued(t, inv, store.state, done)
		cancelQueuedCall(t, inv, done)
	}
	meta, _, err := videoPptService(store.state).SubtitleTask(id)
	if err != nil || meta.ASRLanguage != "en" || store.picker.calls != 1 {
		t.Fatal(meta, err, store.picker.calls)
	}
	_, err = inv.Invoke(store, "video-ppt:subtitle-transcribe-local", json.RawMessage(`{"operationId":"`+id+`","asrLanguage":"auto"}`))
	if err == nil || store.picker.calls != 1 {
		t.Fatal("changed identity repicked", err, store.picker.calls)
	}
}

func TestPreparedASRLanguageMatchingAndLegacyCheckpointPolicy(t *testing.T) {
	for _, language := range []string{"zh", "en", "auto"} {
		for _, legacy := range []bool{false, true} {
			t.Run(language+map[bool]string{true: "-legacy", false: "-v2"}[legacy], func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				state := &contract.State{LibraryRoot: root}
				inv := NewInvoker()
				task, _, err := inv.createQueuedVideoTaskLanguage(state, "", "https://www.bilibili.com/video/BV1xx411c7mD/", "subtitle", "", "", false, &language)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(task.Path, []byte("video"), 0600); err != nil {
					t.Fatal(err)
				}
				srt := "1\n00:00:00,000 --> 00:00:01,000\nSaved words\n"
				if legacy {
					err = videoppt.SavePreparedSubtitle(task, srt, 1)
				} else {
					hash, _ := videoppt.VideoDigest(task.Path)
					provenance := &contract.TranscriptionProvenance{Version: 1, RequestedLanguage: language, EngineAdapter: "native-jsonrpc/asr.transcribe", AdapterSchema: 1, RuntimeVersionState: "unknown", DeclaredModelLabel: "whisper-small-onnx-q8", ModelIdentityState: "not-attested", AcquisitionSHA256: strings.Repeat("a", 64)}
					sum := sha256.Sum256([]byte(srt))
					err = videoppt.SavePreparedSubtitleWithProvenance(task, srt, 1, hash, hex.EncodeToString(sum[:]), provenance)
				}
				if err != nil {
					t.Fatal(err)
				}
				result, recovered, err := inv.preparedVideoSubtitle(context.Background(), state, task)
				if legacy && language != "zh" {
					if err == nil {
						t.Fatal("unattested legacy language reused")
					}
					return
				}
				if err != nil || !recovered || result.SRT != srt {
					t.Fatal(result, recovered, err)
				}
				if !legacy {
					// Task and operation still agree: a mismatching provenance alone
					// must fail, rather than being accepted as a valid V2 checkpoint.
					checkpoint := filepath.Join(task.Dir, "subtitle-prepared.json")
					original, readErr := os.ReadFile(checkpoint)
					if readErr != nil {
						t.Fatal(readErr)
					}
					var evidence map[string]any
					if err := json.Unmarshal(original, &evidence); err != nil {
						t.Fatal(err)
					}
					wrong := "auto"
					if language == "auto" {
						wrong = "en"
					}
					evidence["provenance"].(map[string]any)["requestedLanguage"] = wrong
					changed, _ := json.Marshal(evidence)
					if err := os.WriteFile(checkpoint, changed, 0600); err != nil {
						t.Fatal(err)
					}
					if _, _, err := inv.preparedVideoSubtitle(context.Background(), state, task); err == nil {
						t.Fatal("wrong provenance language reused")
					}
					if err := os.WriteFile(checkpoint, original, 0600); err != nil {
						t.Fatal(err)
					}
					// Corrupt only the task binding is rejected before any native call.
					path := filepath.Join(task.Dir, "task.json")
					raw, _ := os.ReadFile(path)
					var meta map[string]any
					_ = json.Unmarshal(raw, &meta)
					meta["asrLanguage"] = "auto"
					if language == "auto" {
						meta["asrLanguage"] = "en"
					}
					body, _ := json.Marshal(meta)
					_ = os.WriteFile(path, body, 0600)
					if _, _, err := inv.preparedVideoSubtitle(context.Background(), state, task); err == nil {
						t.Fatal("mismatched task reused")
					}
				}
			})
		}
	}
}

func TestSavedNondefaultLanguageRequiresCheckpointButLegacyChineseStillRecovers(t *testing.T) {
	for _, language := range []string{"zh", "en", "auto"} {
		t.Run(language, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			state := &contract.State{LibraryRoot: root}
			inv := NewInvoker()
			task, _, err := inv.createQueuedVideoTaskLanguage(state, "", "https://www.bilibili.com/video/BV1xx411c7mD/", "subtitle", "", "", false, &language)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := library.CourseDirectory(root, "Transcripts", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "saved.srt")
			if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:00:01,000\nSaved words\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := videoPptService(state).MarkSubtitleSaved(task.ID, path); err != nil {
				t.Fatal(err)
			}
			params, _ := json.Marshal(map[string]string{"id": task.ID})
			_, err = inv.handleVideoResumeSubtitleContext(context.Background(), nil, state, params)
			if language == "zh" {
				if err != nil {
					t.Fatal("legacy Chinese no longer recovers", err)
				}
			} else {
				if err == nil {
					t.Fatal("missing nondefault language evidence accepted")
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("saved output lost", err)
				}
				if _, err := os.Stat(task.Dir); err != nil {
					t.Fatal("task lost", err)
				}
			}
		})
	}
}

func TestSavedStationSubtitleDoesNotRequireASRLanguageEvidence(t *testing.T) {
	for _, language := range []string{"en", "auto"} {
		t.Run(language, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			state := &contract.State{LibraryRoot: root}
			inv := NewInvoker()
			task, _, err := inv.createQueuedVideoTaskLanguage(state, "", "https://www.bilibili.com/video/BV1234567890/?p=2", "subtitle-station", "", "", false, &language)
			if err != nil {
				t.Fatal(err)
			}
			service := videoPptService(state)
			if err := service.SaveStationSubtitle(task, videoppt.StationSubtitle{State: "prepared", Title: "station", Source: "中文", SRT: stationFixtureSRT, Count: 1}); err != nil {
				t.Fatal(err)
			}
			meta, _, err := service.SubtitleTask(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := library.CourseDirectory(root, "Transcripts", "")
			if err != nil {
				t.Fatal(err)
			}
			path, err := videoppt.PublishSubtitle(context.Background(), task, directory, meta.OutputName, []byte(stationFixtureSRT))
			if err != nil {
				t.Fatal(err)
			}
			if err := service.MarkSubtitleSaved(task.ID, path); err != nil {
				t.Fatal(err)
			}
			params, _ := json.Marshal(map[string]string{"id": task.ID})
			value, err := NewInvoker().handleVideoResumeSubtitleContext(context.Background(), nil, state, params)
			if err != nil {
				t.Fatal("verified platform subtitles wrongly require ASR evidence", err)
			}
			output := value.(map[string]any)
			if output["savedPath"] != path || output["provenance"] != nil {
				t.Fatal("platform output got invented ASR provenance", output)
			}
		})
	}
}

func TestSubtitleBatchChildrenKeepBoundLanguageAfterReconstruction(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	service := videoPptService(state)
	input := videoppt.BatchInput{Kind: "subtitle", URL: "https://www.bilibili.com/video/BV1xx411c7mD/", Pages: []int{1}, Prompt: "station-first-then-asr", ASRLanguage: "auto"}
	batch, err := service.BindBatch("", input)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = service.SetBatchEpisodes(batch.ID, []videoppt.BatchEpisode{{Page: 1, Title: "one", URL: input.URL}})
	if err != nil {
		t.Fatal(err)
	}
	inv := NewInvoker()
	holdQueueWorker(t, inv)
	store := &subtitleStore{state: state}
	// A fresh invoker binds a child from the persisted parent; it is cancelled
	// before catalog/network/model work, so the identity test is deterministic.
	batch, err = videoPptService(state).Batch(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := inv.runVideoBatchEpisode(context.Background(), store, state, batch, batch.Episodes[0])
		done <- err
	}()
	child := batch.ChildID(1)
	deadline := time.Now().Add(5 * time.Second)
	queued := false
	for !queued {
		select {
		case err := <-done:
			t.Fatalf("child ended before admission: %v", err)
		default:
		}
		value, err := inv.handleVideoPptPending(state)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range value.([]videoppt.Pending) {
			if row.ID == child && row.QueueState == "queued" {
				queued = true
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("child never queued")
		}
		if !queued {
			time.Sleep(time.Millisecond)
		}
	}
	cancelQueuedCall(t, inv, done)
	operation, err := service.Operation(child)
	if err != nil || operation.ASRLanguage != "auto" {
		t.Fatal(operation, err)
	}
	meta, _, err := service.SubtitleTask(child)
	if err != nil || meta.ASRLanguage != "auto" {
		t.Fatal(meta, err)
	}
	params, _ := json.Marshal(map[string]string{"operationId": batch.ID})
	restored, err := NewInvoker().bindVideoBatch(store, state, params, "subtitle")
	if err != nil || restored.Input.ASRLanguage != "auto" {
		t.Fatal(restored, err)
	}
	params, _ = json.Marshal(map[string]string{"operationId": batch.ID, "asrLanguage": "en"})
	if _, err := NewInvoker().bindVideoBatch(store, state, params, "subtitle"); err == nil {
		t.Fatal("explicit batch language change accepted")
	}
}
