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

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func TestPreparedAPIProvenanceRecoveryWithoutCurrentModels(t *testing.T) {
	for _, entry := range []string{"prepared", "transcribe", "saved-handler", "saved-cleaned-handler", "saved-cleaned-legacy", "saved-output-mismatch", "different-language", "legacy"} {
		t.Run(entry, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			state := &contract.State{LibraryRoot: root, ResourcesRoot: filepath.Join(root, "not-installed")}
			source := filepath.Join(root, "input.mp4")
			if err := os.WriteFile(source, []byte("opaque source fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			task, err := videoPptService(state).CreateSubtitleTask(source, "recording.srt", "", 1)
			if err != nil {
				t.Fatal(err)
			}
			srt := "1\n00:00:00,100 --> 00:00:01,100\nSaved words\n"
			sourceHash, _ := videoppt.VideoDigest(task.Path)
			sum := sha256.Sum256([]byte(srt))
			srtHash := hex.EncodeToString(sum[:])
			provenance := &contract.TranscriptionProvenance{Version: 1, RequestedLanguage: "zh", EngineAdapter: "native-jsonrpc/asr.transcribe", AdapterSchema: 1, RuntimeVersionState: "unknown", DeclaredModelLabel: "whisper-small-onnx-q8", ModelIdentityState: "not-attested", AcquisitionSHA256: strings.Repeat("a", 64)}
			if entry == "different-language" {
				provenance.RequestedLanguage = "en"
			}
			if entry == "legacy" || entry == "saved-cleaned-legacy" {
				err = videoppt.SavePreparedSubtitle(task, srt, 1)
			} else {
				err = videoppt.SavePreparedSubtitleWithProvenance(task, srt, 1, sourceHash, srtHash, provenance)
			}
			if err != nil {
				t.Fatal(err)
			}
			inv := NewInvoker() // No native ASR configured; only immutable prepared recovery.
			if entry == "different-language" {
				if _, found, err := inv.preparedVideoSubtitle(context.Background(), state, task); err == nil || !found {
					t.Fatal("cross-language prepared reuse accepted", found, err)
				}
				return
			}
			if entry == "prepared" || entry == "legacy" {
				result, found, err := inv.preparedVideoSubtitle(context.Background(), state, task)
				if err != nil || !found || result.SRT != srt {
					t.Fatal(result, found, err)
				}
				if entry == "legacy" {
					if result.Provenance != nil || result.SRTSHA256 != "" {
						t.Fatal("legacy upgraded")
					}
				} else if result.Provenance.RequestedLanguage != "zh" || result.SourceSHA256 != sourceHash || result.SRTSHA256 != srtHash {
					t.Fatal("evidence missing", result)
				}
				return
			}
			var result any
			var runErr error
			if strings.HasPrefix(entry, "saved-") {
				meta, _, _ := videoPptService(state).SubtitleTask(task.ID)
				directory, err := library.CourseDirectory(root, "Transcripts", "")
				if err != nil {
					t.Fatal(err)
				}
				savedText := srt
				if entry == "saved-output-mismatch" {
					savedText = strings.ReplaceAll(srt, "Saved words", "Different words")
				}
				saved, err := videoppt.PublishSubtitle(context.Background(), task, directory, meta.OutputName, []byte(savedText))
				if err != nil {
					t.Fatal(err)
				}
				if err := videoPptService(state).MarkSubtitleSaved(task.ID, saved); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(entry, "saved-cleaned") {
					if err := os.Remove(task.Path); err != nil {
						t.Fatal(err)
					}
				}
				params, _ := json.Marshal(map[string]string{"id": task.ID})
				result, runErr = inv.handleVideoResumeSubtitleContext(context.Background(), nil, state, params)
			} else {
				result, runErr = inv.transcribeVideoTask(context.Background(), state, task)
			}
			if entry == "saved-output-mismatch" {
				if runErr == nil {
					t.Fatal("independently valid saved/prepared outputs mismatched silently")
				}
				return
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			body, _ := json.Marshal(result)
			if entry == "saved-cleaned-legacy" {
				if strings.Contains(string(body), `"provenance"`) {
					t.Fatal("legacy saved recovery fabricated evidence")
				}
				return
			}
			if !strings.Contains(string(body), `"requestedLanguage":"zh"`) || !strings.Contains(string(body), sourceHash) || !strings.Contains(string(body), srtHash) {
				t.Fatalf("API output dropped evidence %s", body)
			}
			if entry == "saved-cleaned-handler" && !strings.Contains(string(body), `"sourceVerification":"historical"`) {
				t.Fatal("removed source implied current verification")
			}
		})
	}
}
