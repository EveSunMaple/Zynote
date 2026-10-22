package courseware

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Provenance string

const (
	ProvenanceLocal  Provenance = "local"
	ProvenanceOCR    Provenance = "ocr"
	ProvenanceManual Provenance = "manual"
)

type SourceIdentity struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type CacheMetadata struct {
	Version    int      `json:"version"`
	SourcePath string   `json:"sourcePath"`
	SourceSha  string   `json:"sourceSha256"`
	SourceSize int64    `json:"sourceSize"`
	TextSha    string   `json:"textSha256"`
	Provenance string   `json:"provenance"`
	NeedsOcr   bool     `json:"needsOcr"`
	Omissions  []string `json:"omissions"`
	CapturedAt string   `json:"capturedAt"`
}

type ParsedCache struct {
	Text       string         `json:"text"`
	State      string         `json:"state"`
	Provenance string         `json:"provenance"`
	Metadata   *CacheMetadata `json:"metadata,omitempty"`
	NeedsOcr   bool           `json:"needsOcr"`
	Omissions  []string       `json:"omissions"`
}

const CacheMarker = "bilisum-courseware-cache:v1:"

func Hash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func TextHash(text string) string { return Hash([]byte(text)) }

func ReadSourceIdentity(path string) (SourceIdentity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SourceIdentity{}, fmt.Errorf("课件文件不可读")
	}
	return SourceIdentity{Path: path, Sha256: Hash(raw), Size: int64(len(raw))}, nil
}

func CreateMetadata(source SourceIdentity, text string, provenance string, needsOcr bool, omissions []string) CacheMetadata {
	unique := []string{}
	seen := map[string]bool{}
	for _, value := range omissions {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return CacheMetadata{
		Version:    1,
		SourcePath: source.Path,
		SourceSha:  source.Sha256,
		SourceSize: source.Size,
		TextSha:    TextHash(text),
		Provenance: provenance,
		NeedsOcr:   needsOcr,
		Omissions:  unique,
		CapturedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}
}

func SerializeCache(text string, metadata CacheMetadata) string {
	metadata.TextSha = TextHash(text)
	encoded, _ := json.Marshal(metadata)
	return text + "\n<!-- " + CacheMarker + base64.StdEncoding.EncodeToString(encoded) + " -->"
}

func SerializeSourceCache(path, text, provenance string, needsOcr bool, omissions []string) (string, error) {
	source, err := ReadSourceIdentity(path)
	if err != nil {
		return "", err
	}
	return SerializeCache(text, CreateMetadata(source, text, provenance, needsOcr, omissions)), nil
}

var cachePattern = regexp.MustCompile(`\n?<!-- ` + regexp.QuoteMeta(CacheMarker) + `([A-Za-z0-9+/=]+) -->\s*$`)

func validMetadata(metadata *CacheMetadata) bool {
	shaPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	return metadata.Version == 1 &&
		metadata.SourcePath != "" &&
		shaPattern.MatchString(metadata.SourceSha) &&
		metadata.SourceSize >= 0 &&
		shaPattern.MatchString(metadata.TextSha) &&
		(metadata.Provenance == "local" || metadata.Provenance == "ocr" || metadata.Provenance == "manual") &&
		metadata.Omissions != nil
}

func ParseCache(value string) ParsedCache {
	match := cachePattern.FindStringIndex(value)
	if match == nil {
		return ParsedCache{Text: value, State: "unknown", Provenance: "unknown", Omissions: []string{}}
	}
	submatch := cachePattern.FindStringSubmatch(value)
	decoded, err := base64.StdEncoding.DecodeString(submatch[1])
	var metadata CacheMetadata
	if err != nil || json.Unmarshal(decoded, &metadata) != nil || !validMetadata(&metadata) {
		return ParsedCache{Text: value, State: "unknown", Provenance: "unknown", Omissions: []string{}}
	}
	text := value[:match[0]]
	state := "unknown"
	if TextHash(text) != metadata.TextSha {
		state = "stale"
	}
	return ParsedCache{
		Text:       text,
		State:      state,
		Provenance: metadata.Provenance,
		Metadata:   &metadata,
		NeedsOcr:   metadata.NeedsOcr,
		Omissions:  append([]string{}, metadata.Omissions...),
	}
}

func Limitations(cache ParsedCache) []string {
	result := append([]string{}, cache.Omissions...)
	if cache.State == "stale" {
		result = append(result, "已保存的课件文本或来源文件与提取记录不匹配；保留现有文本供参考，来源需要核对。")
	} else if cache.State == "unknown" && strings.TrimSpace(cache.Text) != "" {
		result = append(result, "课件文本没有可验证的来源记录；已保留原文本，来源链未核实。")
	}
	if cache.NeedsOcr {
		result = append(result, "课件来源需要 OCR；当前文字可能不完整。")
	}
	if cache.Provenance == "ocr" {
		result = append(result, "课件文字由 OCR 识别，未经人工逐页核对。")
	}
	unique := []string{}
	seen := map[string]bool{}
	for _, value := range result {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique
}

var headingSplit = regexp.MustCompile(`\n(#{1,6}\s|第[一二三四五六七八九十\d]+[章节])|\n\s*\n`)

func splitParagraphs(text string) []string {
	result := []string{}
	last := 0
	for _, match := range headingSplit.FindAllStringSubmatchIndex(text, -1) {
		start := match[1]
		if match[2] >= 0 {
			start = match[0] + 1
		}
		if start > last {
			result = append(result, text[last:start])
		}
		last = start
	}
	if last < len(text) {
		result = append(result, text[last:])
	}
	return result
}

func Chunks(text string, size int) []string {
	if size == 0 {
		size = 14000
	}
	paragraphs := splitParagraphs(text)
	result := []string{}
	current := ""
	for _, para := range paragraphs {
		if len(current)+len(para) > size && current != "" {
			result = append(result, current)
			current = ""
		}
		if len(para) > size {
			if current != "" {
				result = append(result, current)
				current = ""
			}
			for index := 0; index < len(para); index += size {
				end := index + size
				if end > len(para) {
					end = len(para)
				}
				result = append(result, para[index:end])
			}
		} else {
			if current != "" {
				current += "\n\n"
			}
			current += para
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

var _ = filepath.Join
