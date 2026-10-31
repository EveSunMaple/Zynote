package visual

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/framecandidate"
)

type ExtractionReceipt struct {
	Version      int    `json:"version"`
	ID           string `json:"id"`
	SourceHash   string `json:"sourceHash"`
	FrameHash    string `json:"frameHash"`
	TimeMs       int64  `json:"timeMs"`
	CandidateID  string `json:"candidateId"`
	ManifestHash string `json:"manifestHash"`
}

type RecognitionReceipt struct {
	Version      int    `json:"version"`
	ID           string `json:"id"`
	ExtractionID string `json:"extractionId"`
	ResponseHash string `json:"responseHash"`
	Connection   string `json:"connection"`
}

type CoverageLevel struct {
	State  string `json:"state"`
	Count  int    `json:"recordedCount"`
	Total  int    `json:"total"`
	Reason string `json:"reason,omitempty"`
}

type Coverage struct {
	Extracted  CoverageLevel `json:"extracted"`
	Recognized CoverageLevel `json:"recognized"`
	Submitted  CoverageLevel `json:"submitted"`
	Cited      CoverageLevel `json:"cited"`
}

func hasFrameReceipts(task *Task) bool {
	for _, frame := range task.Frames {
		if frame.Extraction != nil || frame.Recognition != nil {
			return true
		}
	}
	return false
}

func extractionID(receipt ExtractionReceipt) string {
	receipt.ID = ""
	body, _ := json.Marshal(receipt)
	return digestText(string(body))
}
func recognitionID(receipt RecognitionReceipt) string {
	receipt.ID = ""
	body, _ := json.Marshal(receipt)
	return digestText(string(body))
}

func recordExtractions(frames []Frame, reference *framecandidate.Reference, manifest framecandidate.Manifest) error {
	if reference == nil || len(frames) != len(manifest.Samples) {
		return fmt.Errorf("画面提取凭据数量不一致")
	}
	for index := range frames {
		frame, sample := &frames[index], manifest.Samples[index]
		if int64(math.Round(frame.Time*1000)) != sample.TimeMs {
			return fmt.Errorf("画面提取凭据时间不一致")
		}
		hash, err := fileDigest(frame.Path)
		if err != nil || hash != frame.Hash {
			return fmt.Errorf("提取画面已经改变，未记录提取凭据")
		}
		receipt := ExtractionReceipt{Version: 1, SourceHash: reference.Binding.SourceHash, FrameHash: frame.Hash, TimeMs: sample.TimeMs, CandidateID: sample.ID, ManifestHash: reference.SHA256}
		receipt.ID = extractionID(receipt)
		frame.Extraction = &receipt
	}
	return nil
}

func validExtraction(task *Task, frame Frame, manifest *framecandidate.Manifest) bool {
	receipt := frame.Extraction
	if receipt == nil || manifest == nil || task.CandidateEvidence == nil || receipt.Version != 1 || receipt.ID != extractionID(*receipt) || receipt.SourceHash != task.SourceHash || receipt.FrameHash != frame.Hash || receipt.TimeMs != int64(math.Round(frame.Time*1000)) || receipt.ManifestHash != task.CandidateEvidence.SHA256 {
		return false
	}
	for _, sample := range manifest.Samples {
		if sample.ID == receipt.CandidateID && sample.TimeMs == receipt.TimeMs {
			return true
		}
	}
	return false
}

func validRecognition(task *Task, frame Frame) bool {
	receipt := frame.Recognition
	return receipt != nil && frame.Extraction != nil && receipt.Version == 1 && receipt.ID == recognitionID(*receipt) && receipt.ExtractionID == frame.Extraction.ID && receipt.Connection == task.Connection && receipt.ResponseHash == digestText(frame.Text) && strings.TrimSpace(frame.Text) != "" && frame.Status == StatusDone
}

func (service *Service) validateFrameReceipts(task *Task) error {
	manifest, err := service.readCandidateEvidence(task)
	if err != nil {
		return err
	}
	hasReceipts := false
	for _, frame := range task.Frames {
		if frame.Extraction != nil || frame.Recognition != nil {
			hasReceipts = true
		}
		if frame.Extraction != nil {
			if !validExtraction(task, frame, manifest) {
				return badRequest("画面提取凭据绑定失效")
			}
			if !task.Cleaned {
				if hash, err := fileDigest(frame.Path); err != nil || hash != frame.Hash {
					return badRequest("提取画面已改变，凭据失效")
				}
			}
		}
		if frame.Recognition != nil && (!validExtraction(task, frame, manifest) || !validRecognition(task, frame)) {
			return badRequest("画面识别凭据绑定失效")
		}
	}
	if hasReceipts && task.CoverageInvalidation != "" {
		return badRequest("成果恢复后原覆盖凭据已失效，请保留记录并重新建立任务")
	}
	if (task.Cleaned || task.OutputHash != "") && hasReceipts {
		output, err := service.outputPath(*task)
		if err != nil {
			return err
		}
		hash, err := service.optionalDigest(output)
		if err != nil || task.OutputHash == "" || hash != task.OutputHash {
			return badRequest("保留识别凭据的成果已改变")
		}
	}
	return nil
}

func unknownCoverage(total int) *Coverage {
	unknown := CoverageLevel{State: "unknown", Total: total, Reason: "missing-receipts"}
	notIntegrated := CoverageLevel{State: "unknown", Total: total, Reason: "not-integrated"}
	return &Coverage{Extracted: unknown, Recognized: unknown, Submitted: notIntegrated, Cited: notIntegrated}
}

func coverageLevel(count, total int) CoverageLevel {
	level := CoverageLevel{State: "unknown", Count: count, Total: total, Reason: "not-recorded"}
	if count > 0 {
		level.State = "partial"
		level.Reason = ""
		if count == total {
			level.State = "recorded"
		}
	}
	return level
}

// Read-only projection. Legacy statuses are not retroactive receipts. Retained
// cleaned frames rely on their historical receipt and verified output digest.
func (service *Service) projectCoverage(task *Task) *Coverage {
	coverage := unknownCoverage(len(task.Frames))
	hasReceipts := false
	for _, frame := range task.Frames {
		if frame.Extraction != nil || frame.Recognition != nil {
			hasReceipts = true
			break
		}
	}
	if !hasReceipts {
		return coverage
	}
	source, err := fileDigest(task.Source)
	if err != nil || source != task.SourceHash || service.validateFrameReceipts(task) != nil {
		coverage.Extracted.State, coverage.Recognized.State = "invalid", "invalid"
		coverage.Extracted.Reason, coverage.Recognized.Reason = "binding-invalid", "binding-invalid"
		if task.CoverageInvalidation == "output-recovery-unverified" {
			coverage.Extracted.Reason, coverage.Recognized.Reason = task.CoverageInvalidation, task.CoverageInvalidation
		}
		return coverage
	}
	extracted, recognized := 0, 0
	for _, frame := range task.Frames {
		if frame.Extraction != nil {
			extracted++
		}
		if frame.Recognition != nil {
			recognized++
		}
	}
	coverage.Extracted = coverageLevel(extracted, len(task.Frames))
	coverage.Recognized = coverageLevel(recognized, len(task.Frames))
	return coverage
}

func coverageDisplay(level CoverageLevel) string {
	switch level.State {
	case "recorded", "partial":
		return fmt.Sprintf("%d/%d 个所选画面", level.Count, level.Total)
	case "invalid":
		if level.Reason == "output-recovery-unverified" {
			return "记录失效；恢复文件已生成，历史覆盖记录需重新核对"
		}
		return "记录失效，请核对来源与成果"
	default:
		return "未记录"
	}
}

func coverageText(task *Task) string {
	coverage := task.Coverage
	if coverage == nil {
		coverage = unknownCoverage(len(task.Frames))
	}
	return "## 事实覆盖\n\n- 已提取：" + coverageDisplay(coverage.Extracted) + "\n- 已识别并保存文字：" + coverageDisplay(coverage.Recognized) + "\n- 已送入摘要生成：未记录（尚未接入）\n- 已被摘要引用：未记录（尚未接入）\n\n分母仅为所选画面数，不代表整段视频；识别记录不代表文字正确率。\n\n"
}
