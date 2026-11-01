package videoppt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type PreparedSubtitle struct {
	Version     int                               `json:"version"`
	VideoSHA256 string                            `json:"videoSHA256"`
	SRT         string                            `json:"srt"`
	Count       int                               `json:"count"`
	SRTSHA256   string                            `json:"srtSHA256,omitempty"`
	Provenance  *contract.TranscriptionProvenance `json:"provenance,omitempty"`
}

var subtitleDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validSubtitleProvenance(value *contract.TranscriptionProvenance) bool {
	return value != nil && value.Version == 1 && value.RequestedLanguage != "" && strings.TrimSpace(value.RequestedLanguage) == value.RequestedLanguage && len(value.RequestedLanguage) <= 32 && value.EngineAdapter == "native-jsonrpc/asr.transcribe" && value.AdapterSchema == 1 && value.RuntimeVersionState == "unknown" && value.DeclaredModelLabel == "whisper-small-onnx-q8" && value.ModelIdentityState == "not-attested" && subtitleDigestPattern.MatchString(value.AcquisitionSHA256)
}
func validatePreparedEvidence(value PreparedSubtitle) error {
	sum := sha256.Sum256([]byte(value.SRT))
	segments, err := documents.ParseSrt(value.SRT)
	if value.Count < 1 || value.SRT == "" || !subtitleDigestPattern.MatchString(value.VideoSHA256) || hex.EncodeToString(sum[:]) != value.SRTSHA256 || !validSubtitleProvenance(value.Provenance) || err != nil || len(segments) != value.Count {
		return requestError("已保存转写凭据无效，请保留任务")
	}
	return nil
}

func SavePreparedSubtitleWithProvenance(task CreatedTask, srt string, count int, sourceHash, srtHash string, provenance *contract.TranscriptionProvenance) error {
	value := PreparedSubtitle{Version: 2, VideoSHA256: sourceHash, SRT: srt, Count: count, SRTSHA256: srtHash, Provenance: provenance}
	if err := validatePreparedEvidence(value); err != nil {
		return err
	}
	current, err := VideoDigest(task.Path)
	if err != nil || current != sourceHash {
		return requestError("保存前视频内容已改变，请保留恢复记录")
	}
	return saveTaskJSON(filepath.Join(task.Dir, subtitlePreparedFileName), value)
}

func SavePreparedSubtitle(task CreatedTask, srt string, count int) error {
	hash, err := VideoDigest(task.Path)
	if err != nil {
		return err
	}
	return saveTaskJSON(filepath.Join(task.Dir, subtitlePreparedFileName), PreparedSubtitle{Version: 1, VideoSHA256: hash, SRT: srt, Count: count})
}

func readPreparedSubtitle(task CreatedTask) (PreparedSubtitle, bool, error) {
	var result PreparedSubtitle
	body, err := os.ReadFile(filepath.Join(task.Dir, subtitlePreparedFileName))
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if json.Unmarshal(body, &result) != nil || (result.Version != 1 && result.Version != 2) || result.VideoSHA256 == "" || result.SRT == "" || result.Count < 1 {
		return result, true, requestError("已保存转写记录无效，请保留任务")
	}
	if result.Version == 1 {
		result.Provenance = nil // Legacy observations cannot be retrofitted.
		result.SRTSHA256 = ""
	} else if err := validatePreparedEvidence(result); err != nil {
		return result, true, err
	}
	return result, true, nil
}

func LoadPreparedSubtitle(task CreatedTask) (PreparedSubtitle, bool, error) {
	result, found, err := readPreparedSubtitle(task)
	if err != nil || !found {
		return result, found, err
	}
	hash, err := VideoDigest(task.Path)
	if err != nil || hash != result.VideoSHA256 {
		return result, true, requestError("视频与已保存转写记录不一致，请保留任务")
	}
	return result, true, nil
}

// The caller has independently verified the published output digest. Cleanup may
// already have removed video.mp4: its hash is historical provenance here.
func LoadPreparedSubtitleForSavedOutput(task CreatedTask, srt string) (PreparedSubtitle, bool, error) {
	result, found, err := readPreparedSubtitle(task)
	if err != nil || !found {
		return result, found, err
	}
	if result.Version == 2 && result.SRT != srt {
		return result, true, requestError("已保存字幕与转写凭据不一致，请保留任务")
	}
	return result, true, nil
}

const subtitlePreparedFileName = "subtitle-prepared.json"
