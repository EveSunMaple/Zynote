package framecandidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
)

type Binding struct {
	SourceHash string `json:"sourceHash"`
	Sampler    string `json:"sampler"`
	Policy     string `json:"policy"`
	Region     [4]int `json:"region"` // x,y,width,height; all zero means full source.
	StepMs     int64  `json:"stepMs"`
	GrayWidth  int    `json:"grayWidth"`
	GrayHeight int    `json:"grayHeight"`
	Cap        int    `json:"cap"`
	DurationMs int64  `json:"durationMs"`
}

type Sample struct {
	ID           string       `json:"id"`
	TimeMs       int64        `json:"timeMs"`
	SampleTimeMs int64        `json:"sampleTimeMs"`
	Reason       string       `json:"reason"`
	GrayHash     string       `json:"sampledGraySHA256,omitempty"`
	Metrics      *GrayMetrics `json:"metrics,omitempty"`
}

type Manifest struct {
	Version int      `json:"version"`
	Stage   string   `json:"stage"`
	Binding Binding  `json:"binding"`
	Samples []Sample `json:"samples"`
}

type Reference struct {
	Binding Binding `json:"binding"`
	SHA256  string  `json:"sha256"`
}

var evidenceHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var evidenceID = regexp.MustCompile(`^[a-f0-9-]{32,36}$`)

func hashBytes(body []byte) string  { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
func GrayHash(pixels []byte) string { return hashBytes(pixels) }

func (binding Binding) valid() bool {
	if !evidenceHash.MatchString(binding.SourceHash) || binding.Sampler == "" || binding.Policy == "" || binding.StepMs < 0 || binding.DurationMs < 0 || binding.Cap < 0 || binding.GrayWidth < 0 || binding.GrayHeight < 0 {
		return false
	}
	for _, value := range binding.Region {
		if value < 0 {
			return false
		}
	}
	return true
}

func sampleID(binding Binding, sample Sample) string {
	body, _ := json.Marshal(struct {
		Binding      Binding
		TimeMs       int64
		SampleTimeMs int64
	}{binding, sample.TimeMs, sample.SampleTimeMs})
	return hashBytes(body)
}

func (manifest Manifest) validate(binding Binding) error {
	if manifest.Version != 1 || manifest.Stage != "selected" || manifest.Binding != binding || !binding.valid() || len(manifest.Samples) < 1 || len(manifest.Samples) > 100000 {
		return fmt.Errorf("候选证据来源、区域或采样版本不一致")
	}
	previous := int64(-1)
	for _, sample := range manifest.Samples {
		if sample.TimeMs < 0 || sample.SampleTimeMs < 0 || (binding.DurationMs > 0 && (sample.TimeMs > binding.DurationMs || sample.SampleTimeMs > binding.DurationMs)) || sample.TimeMs < previous || sample.ID != sampleID(binding, sample) || (sample.GrayHash != "" && !evidenceHash.MatchString(sample.GrayHash)) {
			return fmt.Errorf("候选证据记录无效")
		}
		switch sample.Reason {
		case "stable-initial", "balanced-change", "coverage", "coverage-fallback", "image":
		default:
			return fmt.Errorf("候选选择原因无效")
		}
		if sample.Metrics != nil {
			if sample.Metrics.MeanLight > 255 || sample.Metrics.MeanChange > 255 || sample.Metrics.NormalizedChange > 1 || math.Abs(sample.Metrics.NormalizedChange-sample.Metrics.MeanChange/255) > 1e-12 {
				return fmt.Errorf("候选灰度统计超出物理范围")
			}
			for _, value := range []float64{sample.Metrics.MeanLight, sample.Metrics.MeanChange, sample.Metrics.NormalizedChange} {
				if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
					return fmt.Errorf("候选灰度统计无效")
				}
			}
		}
		previous = sample.TimeMs
	}
	return nil
}

func evidencePath(root, id string, binding Binding) (string, error) {
	if !filepath.IsAbs(root) || !evidenceID.MatchString(id) || !binding.valid() {
		return "", fmt.Errorf("候选证据身份无效")
	}
	body, _ := json.Marshal(binding)
	return filepath.Join(root, ".candidates", id+"-"+hashBytes(body)+".json"), nil
}

// Publish only selection facts. It does not claim extraction, recognition,
// submission or citation. Different explicit crops retain separate manifests.
func Publish(root, id string, binding Binding, samples []Sample) (*Reference, error) {
	path, err := evidencePath(root, id, binding)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{Version: 1, Stage: "selected", Binding: binding, Samples: append([]Sample(nil), samples...)}
	for index := range manifest.Samples {
		manifest.Samples[index].ID = sampleID(binding, manifest.Samples[index])
	}
	if err := manifest.validate(binding); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".candidate-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(body)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return nil, fmt.Errorf("候选证据保存失败")
	}
	if err := os.Link(temporary.Name(), path); err != nil {
		previous, readErr := os.ReadFile(path)
		if readErr != nil || string(previous) != string(body) {
			return nil, fmt.Errorf("已有候选证据内容不同，未覆盖")
		}
	}
	return &Reference{Binding: binding, SHA256: hashBytes(body)}, nil
}

func Read(root, id string, reference Reference, expected Binding) (Manifest, error) {
	if reference.Binding != expected || !evidenceHash.MatchString(reference.SHA256) {
		return Manifest{}, fmt.Errorf("候选证据绑定不一致")
	}
	path, err := evidencePath(root, id, expected)
	if err != nil {
		return Manifest{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 40000000 {
		return Manifest{}, fmt.Errorf("候选证据文件不可用")
	}
	body, err := os.ReadFile(path)
	if err != nil || hashBytes(body) != reference.SHA256 {
		return Manifest{}, fmt.Errorf("候选证据内容已经改变")
	}
	var manifest Manifest
	if json.Unmarshal(body, &manifest) != nil {
		return Manifest{}, fmt.Errorf("候选证据格式无效")
	}
	if err := manifest.validate(expected); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
