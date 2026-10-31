package knowledge

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "Knowledge"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func hash64(value string) string { return sha256Hex(value) }

func expectedRaw(value *string) *json.RawMessage {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(*value)
	raw := json.RawMessage(encoded)
	return &raw
}

func importFixture(t *testing.T, store *Store, locator, title, text string, expected *string) map[string]any {
	t.Helper()
	result, err := store.Import(ImportInput{
		Title: title, Text: text, Locator: locator, SourceHash: hash64(text),
		Category: "课堂", ExpectedGeneration: expectedRaw(expected),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestImportListReadAndGeneration(t *testing.T) {
	store := openTestStore(t)
	text := strings.Repeat("机器学习课堂笔记。", 200)
	result := importFixture(t, store, "https://example.com/lesson", "第一课", text, nil)
	if result["unchanged"] != false {
		t.Fatalf("first import should change: %+v", result)
	}
	entry := result["entry"].(map[string]any)
	id := entry["bvid"].(string)
	if !strings.HasPrefix(id, "LV") || entry["title"] != "第一课" || entry["folder"] != "课堂" ||
		entry["textLength"].(int) != len([]rune(text)) {
		t.Fatalf("entry mismatch: %+v", entry)
	}
	generation, err := store.Generation("https://example.com/lesson")
	if err != nil || generation != entry["generation_id"] {
		t.Fatalf("generation mismatch: %v %v", generation, err)
	}
	missing, err := store.Generation("https://example.com/other")
	if err != nil || missing != nil {
		t.Fatalf("missing generation mismatch: %v %v", missing, err)
	}
	read, err := store.Read(id)
	if err != nil || read["text"] != text {
		t.Fatalf("read mismatch: %v", err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	stats := list["stats"].(map[string]any)
	if stats["totalVideos"] != 1 || stats["totalChunks"] != 2 || list["retrieval"] != "keyword" {
		t.Fatalf("list stats mismatch: %+v", list)
	}
	if _, err := store.Read("LV0000000000000000000000000000000000000000000000000000000000000000"); err == nil ||
		!strings.Contains(err.Error(), "资料已删除") {
		t.Fatalf("missing read mismatch: %v", err)
	}
}

func TestImportUnchangedAndExpectedGeneration(t *testing.T) {
	store := openTestStore(t)
	text := "第一版内容，包含课堂笔记。"
	first := importFixture(t, store, "https://example.com/sync", "同步课", text, nil)
	entry := first["entry"].(map[string]any)
	generation := entry["generation_id"].(string)
	again := importFixture(t, store, "https://example.com/sync", "同步课", text, nil)
	if again["unchanged"] != true {
		t.Fatalf("identical import should be unchanged: %+v", again)
	}
	stale := "stale-generation"
	if _, err := store.Import(ImportInput{
		Title: "同步课", Text: text + "更新", Locator: "https://example.com/sync",
		SourceHash: hash64(text + "更新"), ExpectedGeneration: expectedRaw(&stale),
	}); err == nil || !strings.Contains(err.Error(), "旧同步记录不能覆盖") {
		t.Fatalf("expected generation mismatch: %v", err)
	}
	updated := importFixture(t, store, "https://example.com/sync", "同步课", text+"更新", &generation)
	if updated["unchanged"] != false {
		t.Fatalf("updated import should change: %+v", updated)
	}
	if _, err := store.Import(ImportInput{Title: "同步课", Text: text + "再次", Locator: "https://example.com/sync", SourceHash: hash64(text + "再次")}); err != nil {
		t.Fatalf("omitted expected generation should pass: %v", err)
	}
}

func TestSearchAndCitations(t *testing.T) {
	store := openTestStore(t)
	importFixture(t, store, "https://example.com/a", "课程甲", "第一段：机器学习课堂笔记。第二段：课堂笔记强调特征工程。", nil)
	importFixture(t, store, "https://example.com/b", "课程乙", "hello world machine learning notes", nil)
	result, err := store.Search("课堂笔记", nil)
	if err != nil {
		t.Fatal(err)
	}
	hits := result["hits"].([]any)
	if len(hits) == 0 {
		t.Fatalf("chinese search returned no hits: %+v", result)
	}
	hit := hits[0].(map[string]any)
	if !strings.Contains(hit["content"].(string), "课堂笔记") || hit["bvid"] == "" {
		t.Fatalf("hit mismatch: %+v", hit)
	}
	citation, err := store.Citation(hit["evidence_id"].(string))
	if err != nil || citation["digest"] != hit["digest"] || citation["title"] != hit["title"] {
		t.Fatalf("citation mismatch: %+v %v", citation, err)
	}
	english, err := store.Search("machine learning", nil)
	if err != nil || len(english["hits"].([]any)) == 0 {
		t.Fatalf("english FTS search failed: %+v %v", english, err)
	}
	scoped, err := store.Search("课堂笔记", []string{})
	if err != nil || len(scoped["hits"].([]any)) != 0 {
		t.Fatalf("empty scope mismatch: %+v %v", scoped, err)
	}
	if _, err := store.Search("？", nil); err != nil {
		t.Fatalf("empty terms mismatch: %v", err)
	}
	if _, err := store.db.Exec("UPDATE native_chunks SET content='changed' WHERE evidence_id=?", hit["evidence_id"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Citation(hit["evidence_id"].(string)); err == nil || !strings.Contains(err.Error(), "引用来源已改变") {
		t.Fatalf("tampered citation mismatch: %v", err)
	}
}

func TestOrganizeDeleteAndRebuild(t *testing.T) {
	store := openTestStore(t)
	first := importFixture(t, store, "https://example.com/1", "一", "内容一", nil)["entry"].(map[string]any)
	second := importFixture(t, store, "https://example.com/2", "二", "内容二", nil)["entry"].(map[string]any)
	result, err := store.Organize([]OrganizeItem{
		{ID: first["bvid"].(string), Generation: first["generation_id"].(string)},
		{ID: second["bvid"].(string), Generation: second["generation_id"].(string)},
	}, "复习")
	if err != nil || result["changed"] != 2 {
		t.Fatalf("organize mismatch: %+v %v", result, err)
	}
	again, err := store.Organize([]OrganizeItem{
		{ID: first["bvid"].(string), Generation: first["generation_id"].(string)},
	}, "复习")
	if err != nil || again["unchanged"] != 1 {
		t.Fatalf("organize unchanged mismatch: %+v %v", again, err)
	}
	if _, err := store.Delete(first["bvid"].(string), "wrong"); err == nil {
		t.Fatal("wrong generation delete must fail")
	}
	deleted, err := store.Delete(first["bvid"].(string), first["generation_id"].(string))
	if err != nil || deleted["deleted"] != true {
		t.Fatalf("delete mismatch: %+v %v", deleted, err)
	}
	list, _ := store.List()
	if len(list["entries"].([]any)) != 1 {
		t.Fatalf("delete did not remove entry: %+v", list)
	}
	rebuilt, err := store.Rebuild()
	if err != nil || rebuilt["rebuilt"] != true || rebuilt["totalVideos"] != 1 {
		t.Fatalf("rebuild mismatch: %+v %v", rebuilt, err)
	}
}

func TestClassificationReadAndSave(t *testing.T) {
	store := openTestStore(t)
	entry := importFixture(t, store, "https://example.com/classify", "分类课", "课堂内容", nil)["entry"].(map[string]any)
	draft, err := store.ClassificationRead(entry["bvid"].(string))
	if err != nil {
		t.Fatal(err)
	}
	token := draft["token"].(string)
	selection := `{"type":"教育","topic":"机器学习","subtopics":["特征"],"difficulty":"入门","duration":"短视频","language":"中文","keywords":["课堂"],"quality_tier":"A"}`
	saved, err := store.ClassificationSave(token, json.RawMessage(selection))
	if err != nil {
		t.Fatalf("classification save: %v", err)
	}
	if saved.(map[string]any)["persisted"] != true {
		t.Fatalf("classification result mismatch: %+v", saved)
	}
	if _, err := store.ClassificationSave(token, json.RawMessage(selection)); err == nil ||
		!strings.Contains(err.Error(), "分类草稿已失效") {
		t.Fatalf("reused token mismatch: %v", err)
	}
	read, err := store.Read(entry["bvid"].(string))
	if err != nil {
		t.Fatal(err)
	}
	viewEntry := read["entry"].(map[string]any)
	classification := viewEntry["classification"].(map[string]any)
	if classification["type"] != "教育" || classification["topic"] != "机器学习" {
		t.Fatalf("classification view mismatch: %+v", viewEntry)
	}
	draft, _ = store.ClassificationRead(entry["bvid"].(string))
	if _, err := store.ClassificationSave(draft["token"].(string), json.RawMessage(`{"type":"不存在"}`)); err == nil ||
		!strings.Contains(err.Error(), "分类选项无效") {
		t.Fatalf("invalid selection mismatch: %v", err)
	}
	unknown := `{"type":"教育","topic":"x","subtopics":[],"difficulty":"入门","duration":"短视频","language":"中文","keywords":[],"extra":true}`
	if _, err := store.ClassificationSave(draft["token"].(string), json.RawMessage(unknown)); err == nil {
		t.Fatal("unknown selection key must fail")
	}
}

func vector(direction int) []float64 {
	result := make([]float64, 512)
	result[direction] = 1
	return result
}

func TestVectorStatusPublishAndSemanticSearch(t *testing.T) {
	store := openTestStore(t)
	first := importFixture(t, store, "https://example.com/v1", "向量一", "机器学习特征工程", nil)
	second := importFixture(t, store, "https://example.com/v2", "向量二", "另一段内容", nil)
	_ = first
	status, err := store.EnableVectors()
	if err != nil || !status.Active || status.Index != "not_built" {
		t.Fatalf("enable mismatch: %+v %v", status, err)
	}
	inventory, err := store.Inventory()
	if err != nil || len(inventory) != 2 {
		t.Fatalf("inventory mismatch: %+v %v", inventory, err)
	}
	digest, err := store.InventoryDigest()
	if err != nil {
		t.Fatal(err)
	}
	rows := []VectorRow{}
	for index, row := range inventory {
		rows = append(rows, VectorRow{EvidenceID: row.EvidenceID, Vector: vector(index)})
	}
	_ = second
	status, err = store.PublishVectors(digest, rows)
	if err != nil || status.Index != "ready" || status.VectorCount != 2 {
		t.Fatalf("publish mismatch: %+v %v", status, err)
	}
	result, err := store.SemanticSearch("机器学习特征工程", vector(0), nil)
	if err != nil || len(result["hits"].([]any)) == 0 {
		t.Fatalf("semantic search mismatch: %+v %v", result, err)
	}
	hit := result["hits"].([]any)[0].(map[string]any)
	if hit["bvid"] != inventory[0].SourceID && hit["bvid"] != inventory[1].SourceID {
		t.Fatalf("semantic hit mismatch: %+v", hit)
	}
	if _, err := store.PublishVectors("wrong", rows); err == nil {
		t.Fatal("invalid digest must fail")
	}
	importFixture(t, store, "https://example.com/v3", "向量三", "新增内容", nil)
	stale, err := store.VectorStatus()
	if err != nil || stale.Index != "stale" {
		t.Fatalf("stale mismatch: %+v %v", stale, err)
	}
	fallback, err := store.SemanticSearch("机器学习", vector(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	trace := fallback["trace"].(map[string]any)["vector"].(map[string]any)
	if trace["state"] != "stale" {
		t.Fatalf("fallback trace mismatch: %+v", fallback["trace"])
	}
}
