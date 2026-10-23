package knowledge

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	_ "modernc.org/sqlite"
)

var sourceIDPattern = regexp.MustCompile(`^(?:BV[0-9A-Za-z]{10}|LV(?:[a-f0-9]{8}|[a-f0-9]{64}))$`)
var evidencePattern = regexp.MustCompile(`^evi_[a-f0-9]{32}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var ClassificationOptions = map[string][]string{
	"type": {
		"科技", "娱乐", "教育", "生活", "游戏", "音乐", "影视", "知识科普", "教程", "评测", "新闻", "动漫", "其他", "unknown",
	},
	"difficulty": {"入门", "进阶", "专业", "unknown"},
	"duration":   {"短视频", "中等", "长视频", "超长", "unknown"},
	"language":   {"中文", "英文", "混合", "其他", "unknown"},
}

type Store struct {
	db     *sql.DB
	drafts sync.Mutex
	draft  map[string]draft
}

type draft struct {
	id         string
	generation string
	digest     string
}

type StoredChunk struct {
	EvidenceID       string `json:"evidence_id"`
	SourceID         string `json:"source_id"`
	GenerationID     string `json:"generation_id"`
	SourceRevisionID string `json:"source_revision_id"`
	ChunkIndex       int    `json:"chunk_index"`
	Content          string `json:"content"`
	Digest           string `json:"digest"`
	Locator          string `json:"locator"`
}

type ImportInput struct {
	Title              string           `json:"title"`
	Text               string           `json:"text"`
	Locator            string           `json:"locator"`
	SourceHash         string           `json:"sourceHash"`
	Category           string           `json:"category"`
	SourceMethod       *string          `json:"sourceMethod"`
	Origin             string           `json:"origin"`
	ExpectedGeneration *json.RawMessage `json:"expectedGeneration"`
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("知识库目录不可用")
	}
	path := filepath.Join(root, "knowledge-native.sqlite")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	var existing int
	_ = db.QueryRow("SELECT 1 FROM sqlite_master WHERE name='native_chunks_fts'").Scan(&existing)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS native_sources(id TEXT PRIMARY KEY,generation TEXT NOT NULL,json TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS native_chunks(evidence_id TEXT PRIMARY KEY,source_id TEXT NOT NULL REFERENCES native_sources(id) ON DELETE CASCADE,generation_id TEXT NOT NULL,source_revision_id TEXT NOT NULL,chunk_index INTEGER NOT NULL,content TEXT NOT NULL,digest TEXT NOT NULL,locator TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS native_chunk_source ON native_chunks(source_id);
		CREATE TABLE IF NOT EXISTS native_migrations(name TEXT PRIMARY KEY,json TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS native_vectors(evidence_id TEXT PRIMARY KEY,vector TEXT NOT NULL);
		CREATE VIRTUAL TABLE IF NOT EXISTS native_chunks_fts USING fts5(content,content='native_chunks',content_rowid='rowid',tokenize='trigram');
		CREATE TRIGGER IF NOT EXISTS native_chunks_ai AFTER INSERT ON native_chunks BEGIN INSERT INTO native_chunks_fts(rowid,content) VALUES(new.rowid,new.content); END;
		CREATE TRIGGER IF NOT EXISTS native_chunks_ad AFTER DELETE ON native_chunks BEGIN INSERT INTO native_chunks_fts(native_chunks_fts,rowid,content) VALUES('delete',old.rowid,old.content); END;`); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, draft: map[string]draft{}}
	if existing == 0 {
		if _, err := store.Rebuild(); err != nil {
			db.Close()
			return nil, err
		}
	}
	return store, nil
}

func (store *Store) Close() error { return store.db.Close() }

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func (store *Store) get(id string) (map[string]any, error) {
	return getFrom(store.db, id)
}

func getFrom(db queryer, id string) (map[string]any, error) {
	if !sourceIDPattern.MatchString(id) {
		return nil, fmt.Errorf("资料标识无效")
	}
	var raw string
	err := db.QueryRow("SELECT json FROM native_sources WHERE id=?", id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return nil, fmt.Errorf("资料记录损坏")
	}
	return entry, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func numberValue(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

func selectionDigest(entry map[string]any) string {
	encoded, _ := json.Marshal([]any{
		stringValue(entry["title"]), numberValue(entry["duration"]), stringValue(entry["text"]),
	})
	return sha256Hex(string(encoded))
}

func view(entry map[string]any) map[string]any {
	result := map[string]any{
		"bvid": entry["bvid"], "title": entry["title"], "textLength": entry["textLength"],
		"savedAt": entry["savedAt"], "folder": entry["folder"],
		"source_locator": entry["source_locator"], "sourceHash": entry["sourceHash"],
		"generation_id": entry["generation_id"],
	}
	if method, ok := entry["sourceMethod"].(string); ok && method != "" {
		result["sourceMethod"] = method
	}
	if record, ok := entry["classification"].(map[string]any); ok {
		if stringValue(record["input_digest"]) == selectionDigest(entry) {
			result["classification"] = record["selection"]
		}
	}
	return result
}

func (store *Store) List() (map[string]any, error) {
	rows, err := store.db.Query("SELECT json FROM native_sources ORDER BY json_extract(json,'$.savedAt') DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []any{}
	total := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, fmt.Errorf("资料记录损坏")
		}
		entries = append(entries, view(entry))
		total++
	}
	var chunks int
	if err := store.db.QueryRow("SELECT count(*) FROM native_chunks WHERE chunk_index>=0").Scan(&chunks); err != nil {
		return nil, err
	}
	return map[string]any{
		"entries":   entries,
		"stats":     map[string]any{"totalVideos": total, "totalChunks": chunks},
		"retrieval": "keyword",
	}, nil
}

func (store *Store) Generation(locator string) (any, error) {
	if len([]rune(locator)) < 1 || len([]rune(locator)) > 2000 {
		return nil, fmt.Errorf("资料来源地址无效")
	}
	id := "LV" + sha256Hex(locator)
	entry, err := store.get(id)
	if err != nil || entry == nil {
		return nil, err
	}
	if stringValue(entry["source_locator"]) != locator {
		return nil, nil
	}
	return entry["generation_id"], nil
}

func (store *Store) Read(id string) (map[string]any, error) {
	entry, err := store.get(id)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, fmt.Errorf("资料已删除")
	}
	return map[string]any{"entry": view(entry), "text": entry["text"]}, nil
}

func (store *Store) Import(input ImportInput) (map[string]any, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Category = strings.TrimSpace(input.Category)
	if len([]rune(input.Title)) < 1 || len([]rune(input.Title)) > 300 {
		return nil, fmt.Errorf("资料标题无效")
	}
	if input.Text == "" || len([]rune(input.Text)) > 2_000_000 {
		return nil, fmt.Errorf("资料文字为空或超过上限")
	}
	if len([]rune(input.Locator)) < 1 || len([]rune(input.Locator)) > 2000 || !hashPattern.MatchString(input.SourceHash) {
		return nil, fmt.Errorf("资料来源参数无效")
	}
	if len([]rune(input.Category)) > 100 {
		return nil, fmt.Errorf("资料分类无效")
	}
	if input.Origin == "" {
		input.Origin = "local"
	}
	if input.Origin != "local" && input.Origin != "bilibili-subtitle" && input.Origin != "bilibili-discussion" {
		return nil, fmt.Errorf("资料来源类型无效")
	}
	if strings.TrimSpace(input.Text) == "" {
		return nil, fmt.Errorf("资料文字为空")
	}
	transaction, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()
	id := "LV" + sha256Hex(input.Locator)
	prior, err := getFrom(transaction, id)
	if err != nil {
		return nil, err
	}
	textHash := sha256Hex(input.Text)
	if prior != nil && stringValue(prior["title"]) == input.Title &&
		stringValue(prior["content_hash"]) == textHash &&
		stringValue(prior["folder"]) == input.Category &&
		stringValue(prior["sourceHash"]) == input.SourceHash &&
		sameSourceMethod(prior, input.SourceMethod) {
		return map[string]any{"entry": view(prior), "unchanged": true}, nil
	}
	if input.ExpectedGeneration != nil {
		expected := string(*input.ExpectedGeneration)
		priorGeneration := "null"
		if prior != nil {
			priorGeneration = stringValue(prior["generation_id"])
		}
		if expected == "null" {
			if prior != nil {
				return nil, fmt.Errorf("来源已有更新，旧同步记录不能覆盖当前版本")
			}
		} else {
			var value string
			if err := json.Unmarshal(*input.ExpectedGeneration, &value); err != nil || value != priorGeneration {
				return nil, fmt.Errorf("来源已有更新，旧同步记录不能覆盖当前版本")
			}
		}
	}
	generation := "gen_" + strings.ReplaceAll(ids.New(), "-", "")
	revision := "rev_" + strings.ReplaceAll(ids.New(), "-", "")
	entry := map[string]any{
		"bvid": id, "title": input.Title, "text": input.Text,
		"textLength":     len([]rune(input.Text)),
		"savedAt":        time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"folder":         input.Category,
		"source_locator": input.Locator,
		"sourceHash":     input.SourceHash,
		"generation_id":  generation, "source_revision_id": revision,
		"content_hash": textHash, "source": input.Origin, "source_kind": "local_text_v1",
	}
	if input.SourceMethod != nil {
		entry["sourceMethod"] = *input.SourceMethod
	}
	chunks := buildChunks(entry, generation, revision)
	if err := publish(transaction, entry, chunks); err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"entry": view(entry), "unchanged": false}, nil
}

func sameSourceMethod(entry map[string]any, input *string) bool {
	stored, present := entry["sourceMethod"]
	if input == nil {
		return !present || stored == nil
	}
	value, ok := stored.(string)
	return ok && value == *input
}

func buildChunks(entry map[string]any, generation, revision string) []StoredChunk {
	text := []rune(stringValue(entry["text"]))
	bvid := stringValue(entry["bvid"])
	locator := stringValue(entry["source_locator"])
	chunks := []StoredChunk{}
	for offset, index := 0, 0; offset < len(text); offset, index = offset+900, index+1 {
		end := offset + 1100
		if end > len(text) {
			end = len(text)
		}
		content := string(text[offset:end])
		encoded, _ := json.Marshal([]any{"native-kb-v1", bvid, revision, index})
		digest := sha256Hex(string(encoded))
		chunks = append(chunks, StoredChunk{
			EvidenceID: "evi_" + digest[:32], SourceID: bvid, GenerationID: generation,
			SourceRevisionID: revision, ChunkIndex: index, Content: content,
			Digest: sha256Hex(content), Locator: locator,
		})
	}
	return chunks
}

func publish(transaction *sql.Tx, entry map[string]any, chunks []StoredChunk) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	id := stringValue(entry["bvid"])
	if _, err := transaction.Exec(
		"INSERT OR REPLACE INTO native_sources VALUES(?,?,?)", id, stringValue(entry["generation_id"]), string(raw),
	); err != nil {
		return err
	}
	if _, err := transaction.Exec("DELETE FROM native_chunks WHERE source_id=?", id); err != nil {
		return err
	}
	for _, chunk := range chunks {
		if _, err := transaction.Exec(
			"INSERT INTO native_chunks VALUES(?,?,?,?,?,?,?,?)",
			chunk.EvidenceID, chunk.SourceID, chunk.GenerationID, chunk.SourceRevisionID,
			chunk.ChunkIndex, chunk.Content, chunk.Digest, chunk.Locator,
		); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) Citation(evidenceID string) (map[string]any, error) {
	if !evidencePattern.MatchString(evidenceID) {
		return nil, fmt.Errorf("引用编号无效")
	}
	var chunk StoredChunk
	var raw string
	err := store.db.QueryRow(
		"SELECT c.evidence_id,c.source_id,c.generation_id,c.source_revision_id,c.chunk_index,c.content,c.digest,c.locator,s.json "+
			"FROM native_chunks c JOIN native_sources s ON s.id=c.source_id WHERE c.evidence_id=?",
		evidenceID,
	).Scan(&chunk.EvidenceID, &chunk.SourceID, &chunk.GenerationID, &chunk.SourceRevisionID,
		&chunk.ChunkIndex, &chunk.Content, &chunk.Digest, &chunk.Locator, &raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("引用来源已改变，请重新检索")
	}
	if err != nil {
		return nil, err
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return nil, fmt.Errorf("引用来源已改变，请重新检索")
	}
	if stringValue(entry["generation_id"]) != chunk.GenerationID ||
		stringValue(entry["source_revision_id"]) != chunk.SourceRevisionID ||
		sha256Hex(chunk.Content) != chunk.Digest {
		return nil, fmt.Errorf("引用来源已改变，请重新检索")
	}
	return map[string]any{
		"bvid": chunk.SourceID, "title": entry["title"], "content": chunk.Content,
		"evidence_id": chunk.EvidenceID, "digest": chunk.Digest,
		"generation_id": chunk.GenerationID, "source_revision_id": chunk.SourceRevisionID,
		"chunkIndex": chunk.ChunkIndex, "locator": chunk.Locator,
	}, nil
}

func searchTerms(query string) []string {
	query = strings.TrimSpace(strings.ToLower(query))
	parts := strings.FieldsFunc(query, func(char rune) bool {
		return unicode.IsSpace(char) || strings.ContainsRune(",，。;；?？!！", char)
	})
	seen := map[string]bool{}
	result := []string{}
	for _, part := range parts {
		if part == "" {
			continue
		}
		if !strings.ContainsFunc(part, func(char rune) bool { return unicode.Is(unicode.Han, char) }) {
			if !seen[part] {
				seen[part] = true
				result = append(result, part)
			}
			continue
		}
		characters := []rune(part)
		candidates := []string{part}
		for _, size := range []int{2, 3} {
			for index := 0; index+size <= len(characters); index++ {
				candidates = append(candidates, string(characters[index:index+size]))
			}
		}
		for _, candidate := range candidates {
			if !seen[candidate] {
				seen[candidate] = true
				result = append(result, candidate)
			}
			if len(result) >= 30 {
				break
			}
		}
		if len(result) >= 30 {
			break
		}
	}
	if len(result) > 30 {
		result = result[:30]
	}
	return result
}

func (store *Store) Search(query string, ids []string) (map[string]any, error) {
	if trimmed := strings.TrimSpace(query); len([]rune(trimmed)) < 1 || len([]rune(trimmed)) > 2000 {
		return nil, fmt.Errorf("搜索内容无效")
	}
	for _, id := range ids {
		if !sourceIDPattern.MatchString(id) {
			return nil, fmt.Errorf("资料标识无效")
		}
	}
	terms := searchTerms(query)
	if len(terms) == 0 {
		return map[string]any{"hits": []any{}, "trace": map[string]any{"keyword": map[string]any{"verified": 0}}}, nil
	}
	longTerms := []string{}
	for _, term := range terms {
		if len([]rune(term)) >= 3 {
			longTerms = append(longTerms, term)
		}
	}
	type candidate struct{ evidence, source, content string }
	candidates := []candidate{}
	scope := ""
	args := []any{}
	if ids != nil {
		if len(ids) == 0 {
			scope = " AND 0"
		} else {
			scope = " AND source_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
			for _, id := range ids {
				args = append(args, id)
			}
		}
	}
	if len(longTerms) == len(terms) {
		matchParts := []string{}
		for _, term := range longTerms {
			matchParts = append(matchParts, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
		queryArgs := []any{strings.Join(matchParts, " OR ")}
		queryArgs = append(queryArgs, args...)
		rows, err := store.db.Query(
			"SELECT evidence_id,source_id,content FROM native_chunks WHERE rowid IN "+
				"(SELECT rowid FROM native_chunks_fts WHERE native_chunks_fts MATCH ?) AND chunk_index>=0"+scope+" LIMIT 500",
			queryArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var row candidate
			if err := rows.Scan(&row.evidence, &row.source, &row.content); err != nil {
				return nil, err
			}
			candidates = append(candidates, row)
		}
	} else {
		conditions := []string{}
		queryArgs := []any{}
		for _, term := range terms {
			conditions = append(conditions, "instr(lower(content),?)>0")
			queryArgs = append(queryArgs, term)
		}
		queryArgs = append(queryArgs, args...)
		rows, err := store.db.Query(
			"SELECT evidence_id,source_id,content FROM native_chunks WHERE chunk_index>=0 AND ("+
				strings.Join(conditions, " OR ")+")"+scope+" LIMIT 500", queryArgs...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var row candidate
			if err := rows.Scan(&row.evidence, &row.source, &row.content); err != nil {
				return nil, err
			}
			candidates = append(candidates, row)
		}
	}
	score := func(content string) int {
		lower := strings.ToLower(content)
		count := 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				count++
			}
		}
		return count
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		return score(candidates[left].content) > score(candidates[right].content)
	})
	hits := []any{}
	for _, row := range diverse(candidates, 8, func(value candidate) string { return value.source }) {
		citation, err := store.Citation(row.evidence)
		if err != nil {
			return nil, err
		}
		hits = append(hits, citation)
	}
	return map[string]any{
		"hits": hits,
		"trace": map[string]any{
			"keyword": map[string]any{"verified": len(hits)},
			"vector":  map[string]any{"state": "not_built", "note": "本次使用当前版本的关键词检索。"},
		},
	}, nil
}

func diverse[T any](rows []T, limit int, source func(T) string) []T {
	buckets := map[string][]T{}
	order := []string{}
	for _, row := range rows {
		key := source(row)
		if _, ok := buckets[key]; !ok {
			order = append(order, key)
		}
		buckets[key] = append(buckets[key], row)
	}
	result := []T{}
	for round := 0; len(result) < limit; round++ {
		added := false
		for _, key := range order {
			if round < len(buckets[key]) {
				result = append(result, buckets[key][round])
				added = true
				if len(result) == limit {
					return result
				}
			}
		}
		if !added {
			break
		}
	}
	return result
}

type OrganizeItem struct {
	ID         string `json:"id"`
	Generation string `json:"generation"`
}

func (store *Store) Organize(items []OrganizeItem, target string) (map[string]any, error) {
	target = strings.TrimSpace(target)
	if len([]rune(target)) > 100 {
		return nil, fmt.Errorf("目标分类无效")
	}
	if len(items) == 0 || len(items) > 5000 {
		return nil, fmt.Errorf("请选择需要移动的资料")
	}
	transaction, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()
	entries := []map[string]any{}
	seen := map[string]bool{}
	for _, item := range items {
		entry, err := getFrom(transaction, item.ID)
		if err != nil || entry == nil || seen[item.ID] || stringValue(entry["generation_id"]) != item.Generation {
			return nil, fmt.Errorf("资料列表已改变，请刷新后重试")
		}
		seen[item.ID] = true
		entries = append(entries, entry)
	}
	changed, unchanged := 0, 0
	for _, entry := range entries {
		if stringValue(entry["folder"]) == target {
			unchanged++
			continue
		}
		entry["folder"] = target
		raw, _ := json.Marshal(entry)
		if _, err := transaction.Exec("UPDATE native_sources SET json=? WHERE id=?", string(raw), stringValue(entry["bvid"])); err != nil {
			return nil, err
		}
		changed++
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"changed": changed, "unchanged": unchanged, "failures": []any{}}, nil
}

func (store *Store) Delete(id, generation string) (map[string]any, error) {
	if !sourceIDPattern.MatchString(id) || generation == "" {
		return nil, fmt.Errorf("资料标识无效")
	}
	transaction, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()
	entry, err := getFrom(transaction, id)
	if err != nil || entry == nil || stringValue(entry["generation_id"]) != generation {
		return nil, fmt.Errorf("资料版本已更新，请刷新后再确认删除")
	}
	result, err := transaction.Exec("DELETE FROM native_sources WHERE id=? AND generation=?", id, generation)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return nil, fmt.Errorf("资料版本已更新，请刷新后再确认删除")
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "generation_id": generation}, nil
}

func (store *Store) Rebuild() (map[string]any, error) {
	if _, err := store.db.Exec("INSERT INTO native_chunks_fts(native_chunks_fts) VALUES('rebuild')"); err != nil {
		return nil, err
	}
	if _, err := store.db.Exec("REINDEX native_chunk_source"); err != nil {
		return nil, err
	}
	list, err := store.List()
	if err != nil {
		return nil, err
	}
	result := map[string]any{"rebuilt": true}
	for key, value := range list["stats"].(map[string]any) {
		result[key] = value
	}
	return result, nil
}

func (store *Store) ClassificationRead(id string) (map[string]any, error) {
	entry, err := store.get(id)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, fmt.Errorf("资料已删除")
	}
	token := strings.ReplaceAll(ids.New(), "-", "")
	store.drafts.Lock()
	if len(store.draft) >= 100 {
		for key := range store.draft {
			delete(store.draft, key)
			break
		}
	}
	store.draft[token] = draft{
		id: id, generation: stringValue(entry["generation_id"]), digest: stringValue(entry["content_hash"]),
	}
	store.drafts.Unlock()
	text := []rune(stringValue(entry["text"]))
	excerpt := false
	if len(text) > 120000 {
		text = text[:120000]
		excerpt = true
	}
	result := map[string]any{
		"token": token, "title": entry["title"], "text": string(text), "excerpt": excerpt,
	}
	if selection, ok := view(entry)["classification"]; ok {
		result["selection"] = selection
	}
	return result, nil
}

func (store *Store) ClassificationSave(token string, selectionRaw json.RawMessage) (any, error) {
	var selection map[string]any
	if err := json.Unmarshal(selectionRaw, &selection); err != nil {
		return nil, fmt.Errorf("分类选项无效")
	}
	if err := validateSelection(selection); err != nil {
		return nil, err
	}
	store.drafts.Lock()
	current, ok := store.draft[token]
	delete(store.draft, token)
	store.drafts.Unlock()
	if !ok {
		return nil, fmt.Errorf("分类草稿已失效，请重新读取资料")
	}
	transaction, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()
	entry, err := getFrom(transaction, current.id)
	if err != nil || entry == nil ||
		stringValue(entry["generation_id"]) != current.generation ||
		stringValue(entry["content_hash"]) != current.digest {
		return nil, fmt.Errorf("分类期间资料已改变，请重新读取")
	}
	classification := map[string]any{
		"selection": selection, "method": "manual", "input_digest": selectionDigest(entry),
		"classified_from_generation": stringValue(entry["generation_id"]),
		"reason":                     "用户在原生界面核对并保存",
	}
	entry["classification"] = classification
	raw, _ := json.Marshal(entry)
	if _, err := transaction.Exec("UPDATE native_sources SET json=? WHERE id=?", string(raw), stringValue(entry["bvid"])); err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{
		"bvid": entry["bvid"], "classification": classification, "persisted": true, "reused": false,
		"generation_id": entry["generation_id"], "source_revision_id": entry["source_revision_id"],
	}, nil
}

var selectionKeys = map[string]bool{
	"type": true, "topic": true, "subtopics": true, "difficulty": true, "duration": true,
	"language": true, "keywords": true, "quality_tier": true,
}

func ValidateClassification(selection map[string]any) error { return validateSelection(selection) }

func validateSelection(selection map[string]any) error {
	for key := range selection {
		if !selectionKeys[key] {
			return fmt.Errorf("分类选项无效")
		}
	}
	for key, options := range ClassificationOptions {
		value, ok := selection[key].(string)
		valid := false
		if ok {
			for _, option := range options {
				if option == value {
					valid = true
					break
				}
			}
		}
		if !valid {
			return fmt.Errorf("分类选项无效")
		}
	}
	topic, ok := selection["topic"].(string)
	if !ok || len([]rune(topic)) > 80 {
		return fmt.Errorf("分类选项无效")
	}
	subtopics, ok := selection["subtopics"].([]any)
	if !ok || len(subtopics) > 6 {
		return fmt.Errorf("分类选项无效")
	}
	for _, item := range subtopics {
		value, ok := item.(string)
		if !ok || len([]rune(value)) > 60 {
			return fmt.Errorf("分类选项无效")
		}
	}
	keywords, ok := selection["keywords"].([]any)
	if !ok || len(keywords) > 16 {
		return fmt.Errorf("分类选项无效")
	}
	for _, item := range keywords {
		value, ok := item.(string)
		if !ok || len([]rune(value)) > 64 {
			return fmt.Errorf("分类选项无效")
		}
	}
	if tier, ok := selection["quality_tier"]; ok {
		value, ok := tier.(string)
		if !ok || !map[string]bool{"S": true, "A": true, "B": true, "C": true, "unknown": true}[value] {
			return fmt.Errorf("分类选项无效")
		}
	}
	return nil
}

func (store *Store) Stats() (map[string]any, error) {
	list, err := store.List()
	if err != nil {
		return nil, err
	}
	return list["stats"].(map[string]any), nil
}
