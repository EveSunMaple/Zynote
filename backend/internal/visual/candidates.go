package visual

import (
	"github.com/EveSunMaple/Zynote/backend/internal/framecandidate"
	"math"
	"path/filepath"
	"strings"
)

func visualCandidateBinding(hash, path string, cap int, policy string, duration float64) framecandidate.Binding {
	binding := framecandidate.Binding{SourceHash: hash, Sampler: "visual-coverage-v1", Policy: policy, Cap: cap, DurationMs: int64(math.Round(duration * 1000))}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		binding.Sampler = "visual-image-v1"
		return binding
	}
	if policy == "balanced" {
		binding.Sampler = "visual-balanced-gray-v1"
		binding.StepMs = 5000
		binding.GrayWidth, binding.GrayHeight = 32, 18
	}
	return binding
}

func (service *Service) verifyCandidateEvidence(task *Task) error {
	_, err := service.readCandidateEvidence(task)
	return err
}

func (service *Service) readCandidateEvidence(task *Task) (*framecandidate.Manifest, error) {
	if task.CandidateEvidence == nil {
		return nil, nil
	} // Legacy task: no fabricated evidence.
	if task.Preparation == nil {
		return nil, badRequest("候选证据缺少提取参数")
	}
	expected := visualCandidateBinding(task.SourceHash, task.Source, task.Preparation.Cap, task.Preparation.Policy, task.Duration)
	manifest, err := framecandidate.Read(service.Root, task.ID, *task.CandidateEvidence, expected)
	if err != nil {
		return nil, err
	}
	return &manifest, nil
}
