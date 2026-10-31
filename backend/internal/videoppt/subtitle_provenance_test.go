package videoppt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func subtitleEvidenceFixture(t *testing.T) (CreatedTask, string, string, *contract.TranscriptionProvenance) {
	t.Helper()
	root := t.TempDir()
	task := CreatedTask{Dir: root, Path: filepath.Join(root, "video.mp4")}
	if err := os.WriteFile(task.Path, []byte("original source"), 0600); err != nil {
		t.Fatal(err)
	}
	source, _ := VideoDigest(task.Path)
	srt := "1\n00:00:00,100 --> 00:00:01,100\nSaved words\n"
	provenance := &contract.TranscriptionProvenance{Version: 1, RequestedLanguage: "zh", EngineAdapter: "native-jsonrpc/asr.transcribe", AdapterSchema: 1, RuntimeVersionState: "unknown", DeclaredModelLabel: "whisper-small-onnx-q8", ModelIdentityState: "not-attested", AcquisitionSHA256: strings.Repeat("a", 64)}
	return task, srt, source, provenance
}
func testSRTDigest(srt string) string {
	sum := sha256.Sum256([]byte(srt))
	return hex.EncodeToString(sum[:])
}

func TestPreparedV2EvidenceBindsSourceSRTAndSchema(t *testing.T) {
	for _, mutation := range []string{"none", "srt", "source", "provenance", "version", "count"} {
		t.Run(mutation, func(t *testing.T) {
			task, srt, source, provenance := subtitleEvidenceFixture(t)
			if err := SavePreparedSubtitleWithProvenance(task, srt, 1, source, testSRTDigest(srt), provenance); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(task.Dir, "subtitle-prepared.json")
			body, _ := os.ReadFile(path)
			if mutation != "none" {
				var value PreparedSubtitle
				if err := json.Unmarshal(body, &value); err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "srt":
					value.SRT += "changed"
				case "source":
					_ = os.WriteFile(task.Path, []byte("different input"), 0600)
				case "provenance":
					value.Provenance.RuntimeVersionState = "verified"
				case "version":
					value.Version = 3
				case "count":
					value.Count = 2
				}
				encoded, _ := json.Marshal(value)
				if err := os.WriteFile(path, encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
			actual, found, err := LoadPreparedSubtitle(task)
			if mutation == "none" {
				if err != nil || !found || actual.Version != 2 || actual.Provenance.RequestedLanguage != "zh" {
					t.Fatal(actual, found, err)
				}
			} else if err == nil {
				t.Fatal("tampered evidence accepted", mutation)
			}
		})
	}
}
func TestPreparedSaverRejectsChangedSourceOrSRTBeforeWriting(t *testing.T) {
	for _, sourceChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "srt", true: "source"}[sourceChange], func(t *testing.T) {
			task, srt, source, provenance := subtitleEvidenceFixture(t)
			hash := testSRTDigest(srt)
			if sourceChange {
				_ = os.WriteFile(task.Path, []byte("changed source!"), 0600)
			} else {
				srt += "changed"
			}
			if err := SavePreparedSubtitleWithProvenance(task, srt, 1, source, hash, provenance); err == nil {
				t.Fatal("mismatched inputs saved")
			}
			if _, err := os.Stat(filepath.Join(task.Dir, "subtitle-prepared.json")); !os.IsNotExist(err) {
				t.Fatal("invalid record created", err)
			}
		})
	}
}
func TestLegacyPreparedKeepsUnknownProvenanceAndBytes(t *testing.T) {
	task, srt, _, _ := subtitleEvidenceFixture(t)
	if err := SavePreparedSubtitle(task, srt, 1); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(task.Dir, "subtitle-prepared.json")
	body, _ := os.ReadFile(path)
	var injected map[string]any
	if err := json.Unmarshal(body, &injected); err != nil {
		t.Fatal(err)
	}
	injected["provenance"] = map[string]any{"version": 1, "requestedLanguage": "invented"}
	injected["srtSHA256"] = strings.Repeat("b", 64)
	encoded, _ := json.Marshal(injected)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	actual, found, err := LoadPreparedSubtitle(task)
	after, _ := os.ReadFile(path)
	if err != nil || !found || actual.Version != 1 || actual.Provenance != nil || actual.SRTSHA256 != "" || string(before) != string(after) {
		t.Fatal("legacy evidence retrofitted", actual, found, err)
	}
}
