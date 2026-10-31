package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestKnowledgeCrossLanguage(t *testing.T) {
	directory := os.Getenv("ZYNOTE_KNOWLEDGE_DIR")
	if directory == "" {
		t.Skip("未请求跨语言知识库测试")
	}
	store, err := Open(filepath.Join(directory, "Knowledge"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mode := os.Getenv("ZYNOTE_KNOWLEDGE_MODE")
	result := map[string]any{}
	if mode == "read" {
		id := os.Getenv("ZYNOTE_KNOWLEDGE_ID")
		read, err := store.Read(id)
		if err != nil {
			t.Fatal(err)
		}
		search, err := store.Search("跨语言", nil)
		if err != nil {
			t.Fatal(err)
		}
		result["text"] = read["text"]
		result["search"] = search
		citation := map[string]any{}
		if hits, ok := search["hits"].([]any); ok && len(hits) > 0 {
			hit := hits[0].(map[string]any)
			value, err := store.Citation(hit["evidence_id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			citation = value
		}
		result["citation"] = citation
	} else {
		text := "跨语言知识库校验：Go 写入的资料可以被 TypeScript 读取。"
		imported, err := store.Import(ImportInput{
			Title: "Go 资料", Text: text, Locator: "https://example.com/go", SourceHash: sha256Hex(text),
			Category: "跨语言",
		})
		if err != nil {
			t.Fatal(err)
		}
		search, err := store.Search("TypeScript", nil)
		if err != nil {
			t.Fatal(err)
		}
		hits := search["hits"].([]any)
		if len(hits) == 0 {
			t.Fatal("search returned no hits")
		}
		result["entry"] = imported["entry"]
		result["evidence"] = hits[0].(map[string]any)["evidence_id"]
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "go-result.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
