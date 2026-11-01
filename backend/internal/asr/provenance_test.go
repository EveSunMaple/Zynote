package asr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestObservedProvenanceUsesActualNormalizedLanguage(t *testing.T) {
	for _, language := range []string{" en ", ""} {
		t.Run(language, func(t *testing.T) {
			calls := 0
			pipeline, video, _ := contextPipeline(t, "success", func(_ context.Context, _ string, params any, _ int) (json.RawMessage, error) {
				calls++
				expected := "en"
				if language == "" {
					expected = "zh"
				}
				if params.(map[string]any)["language"] != expected {
					t.Fatal("request language mismatch")
				}
				return json.RawMessage(`{"duration":2,"cues":[{"from":0.1,"to":1.1,"content":"observed words"}]}`), nil
			})
			result, err := pipeline.Transcribe(context.Background(), video, language)
			if err != nil {
				t.Fatal(err)
			}
			expected := "en"
			if language == "" {
				expected = "zh"
			}
			source := sha256.Sum256([]byte("video"))
			srt := sha256.Sum256([]byte(result.SRT))
			manifest := sha256.Sum256([]byte("{}"))
			if calls != 1 || result.SourceSHA256 != hex.EncodeToString(source[:]) || result.SRTSHA256 != hex.EncodeToString(srt[:]) || result.Provenance.RequestedLanguage != expected || result.Provenance.AcquisitionSHA256 != hex.EncodeToString(manifest[:]) || result.Provenance.ModelIdentityState != "not-attested" || result.Provenance.RuntimeVersionState != "unknown" {
				t.Fatalf("invented/missing provenance %+v", result)
			}
		})
	}
}

func TestContentDriftWithRestoredMetadataOrChangedAcquisitionStopsPublication(t *testing.T) {
	for _, target := range []string{"source", "acquisition"} {
		t.Run(target, func(t *testing.T) {
			var change func()
			pipeline, video, _ := contextPipeline(t, "success", func(context.Context, string, any, int) (json.RawMessage, error) {
				change()
				return json.RawMessage(`{"duration":2,"cues":[{"from":0.1,"to":1.1,"content":"stale words"}]}`), nil
			})
			path := video
			if target == "acquisition" {
				path = acquisitionPath(pipeline)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			change = func() {
				body := []byte("VIDEO")
				if target == "acquisition" {
					body = []byte("[]")
				}
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			result, err := pipeline.Transcribe(context.Background(), video, "zh")
			if err == nil || result.SRT != "" {
				t.Fatal("content drift accepted", result, err)
			}
			files, _ := filepath.Glob(filepath.Join(pipeline.DataRoot, "asr-tasks", "*", "result.srt"))
			if len(files) != 0 {
				t.Fatal("stale result published", files)
			}
		})
	}
}
