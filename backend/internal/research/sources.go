package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
)

type Binding struct {
	KBRootID         string `json:"kb_root_id"`
	BVID             string `json:"bvid"`
	GenerationID     string `json:"generation_id"`
	SourceRevisionID string `json:"source_revision_id"`
	EvidenceID       string `json:"evidence_id"`
	ChunkIndex       int    `json:"chunkIndex"`
	Digest           string `json:"digest"`
}

type Source struct {
	Binding    Binding `json:"binding"`
	Title      string  `json:"title"`
	Locator    string  `json:"locator"`
	Text       string  `json:"text"`
	SourceHash string  `json:"sourceHash"`
	Generation string  `json:"generation"`
	Excerpt    bool    `json:"excerpt"`
	Quote      string  `json:"quote"`
}

var sourceIDPattern = regexp.MustCompile(`^(?:BV[A-Za-z0-9]{10}|LV(?:[a-f0-9]{8}|[a-f0-9]{64}))$`)
var evidencePattern = regexp.MustCompile(`^evi_[a-f0-9]{32}$`)

type Sources struct {
	knowledge *knowledge.Store
	RootID    string
}

func NewSources(store *knowledge.Store, rootID string) *Sources {
	return &Sources{knowledge: store, RootID: rootID}
}

func (sources *Sources) binding(citation map[string]any) Binding {
	return Binding{
		KBRootID: sources.RootID, BVID: text(citation["bvid"]),
		GenerationID: text(citation["generation_id"]), SourceRevisionID: text(citation["source_revision_id"]),
		EvidenceID: text(citation["evidence_id"]), ChunkIndex: int(number(citation["chunkIndex"])),
		Digest: text(citation["digest"]),
	}
}

func (sources *Sources) Catalog(query string, offset int) (any, error) {
	if len([]rune(query)) > 200 || offset < 0 || offset > 100000 {
		return nil, fmt.Errorf("资料目录参数无效")
	}
	list, err := sources.knowledge.List()
	if err != nil {
		return nil, err
	}
	entries, _ := list["entries"].([]any)
	filtered := []map[string]any{}
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry == nil {
			continue
		}
		haystack := strings.ToLower(text(entry["title"]) + " " + text(entry["folder"]) + " " + text(entry["source_locator"]))
		if strings.Contains(haystack, lowerQuery) {
			filtered = append(filtered, entry)
		}
	}
	inventory, err := sources.knowledge.Inventory()
	if err != nil {
		return nil, err
	}
	firstEvidence := map[string]string{}
	for _, chunk := range inventory {
		if _, exists := firstEvidence[chunk.SourceID]; !exists {
			firstEvidence[chunk.SourceID] = chunk.EvidenceID
		}
	}
	end := offset + 12
	if end > len(filtered) {
		end = len(filtered)
	}
	items := []any{}
	unavailable := 0
	if offset < len(filtered) {
		for _, entry := range filtered[offset:end] {
			evidenceID := firstEvidence[text(entry["bvid"])]
			if evidenceID == "" {
				unavailable++
				continue
			}
			citation, err := sources.knowledge.Citation(evidenceID)
			if err != nil {
				unavailable++
				continue
			}
			kind := text(entry["sourceMethod"])
			if kind == "" {
				kind = "知识库资料"
			}
			items = append(items, map[string]any{
				"binding": sources.binding(citation), "title": entry["title"],
				"kind": kind, "category": entry["folder"],
			})
		}
	} else {
		unavailable = 0
	}
	var next any
	if offset+12 < len(filtered) {
		next = offset + 12
	}
	return map[string]any{
		"kb_root_id": sources.RootID, "items": items, "next_offset": next,
		"total": len(filtered), "unavailable": unavailable,
	}, nil
}

func (sources *Sources) Read(input Binding) (Source, error) {
	if !sourceIDPattern.MatchString(input.BVID) || !evidencePattern.MatchString(input.EvidenceID) ||
		input.KBRootID != sources.RootID {
		return Source{}, fmt.Errorf("所选研究资料属于另一资料库")
	}
	citation, err := sources.knowledge.Citation(input.EvidenceID)
	if err != nil {
		return Source{}, err
	}
	current := sources.binding(citation)
	left, _ := json.Marshal(current)
	right, _ := json.Marshal(input)
	if string(left) != string(right) {
		return Source{}, fmt.Errorf("所选资料或引用版本已改变，请重新选择")
	}
	source, err := sources.knowledge.Read(input.BVID)
	if err != nil {
		return Source{}, err
	}
	entry, _ := source["entry"].(map[string]any)
	content := text(citation["content"])
	if text(entry["generation_id"]) != input.GenerationID || !strings.Contains(text(source["text"]), content) {
		return Source{}, fmt.Errorf("引用不属于当前资料原文")
	}
	sum := sha256.Sum256([]byte(text(source["text"])))
	return Source{
		Binding: input, Title: text(entry["title"]), Locator: text(entry["source_locator"]),
		Text: text(source["text"]), SourceHash: hex.EncodeToString(sum[:]),
		Generation: text(entry["generation_id"]), Excerpt: false, Quote: content,
	}, nil
}

func (sources *Sources) Verify(items []Source) error {
	for _, source := range items {
		current, err := sources.Read(source.Binding)
		if err != nil {
			return fmt.Errorf("研究期间来源已改变，结果未发布")
		}
		if current.SourceHash != source.SourceHash || current.Text != source.Text ||
			current.Locator != source.Locator || current.Title != source.Title {
			return fmt.Errorf("研究期间来源已改变，结果未发布")
		}
	}
	return nil
}

func (sources *Sources) Search(query string, ids []string) ([]Source, error) {
	result, err := sources.knowledge.Search(query, ids)
	if err != nil {
		return nil, err
	}
	hits, _ := result["hits"].([]any)
	seen := map[string]bool{}
	items := []Source{}
	for _, raw := range hits {
		hit, _ := raw.(map[string]any)
		if hit == nil {
			continue
		}
		id := text(hit["bvid"])
		if seen[id] {
			continue
		}
		seen[id] = true
		source, err := sources.Read(sources.binding(hit))
		if err != nil {
			return nil, err
		}
		items = append(items, source)
	}
	return items, nil
}
