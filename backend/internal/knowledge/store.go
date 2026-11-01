package knowledge

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	// Register the SQLite driver used by database/sql.
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
	Query(query string, args ...any) (*sql.Rows, error)
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

func normalizeImportInput(input *ImportInput) error {
	input.Title = strings.TrimSpace(input.Title)
	input.Category = strings.TrimSpace(input.Category)
	if len([]rune(input.Title)) < 1 || len([]rune(input.Title)) > 300 {
		return fmt.Errorf("资料标题无效")
	}
	if input.Text == "" || len([]rune(input.Text)) > 2_000_000 {
		return fmt.Errorf("资料文字为空或超过上限")
	}
	if len([]rune(input.Locator)) < 1 || len([]rune(input.Locator)) > 2000 || !hashPattern.MatchString(input.SourceHash) {
		return fmt.Errorf("资料来源参数无效")
	}
	if len([]rune(input.Category)) > 100 {
		return fmt.Errorf("资料分类无效")
	}
	if input.Origin == "" {
		input.Origin = "local"
	}
	if input.Origin != "local" && input.Origin != "bilibili-subtitle" && input.Origin != "bilibili-discussion" {
		return fmt.Errorf("资料来源类型无效")
	}
	if strings.TrimSpace(input.Text) == "" {
		return fmt.Errorf("资料文字为空")
	}
	return nil
}

func unchangedImport(prior map[string]any, input ImportInput, textHash string) bool {
	return prior != nil && stringValue(prior["title"]) == input.Title &&
		stringValue(prior["content_hash"]) == textHash &&
		stringValue(prior["folder"]) == input.Category &&
		stringValue(prior["sourceHash"]) == input.SourceHash &&
		sameSourceMethod(prior, input.SourceMethod)
}

func checkImportGeneration(prior map[string]any, expected *json.RawMessage) error {
	if expected == nil {
		return nil
	}
	value := string(*expected)
	priorGeneration := "null"
	if prior != nil {
		priorGeneration = stringValue(prior["generation_id"])
	}
	if value == "null" {
		if prior != nil {
			return fmt.Errorf("来源已有更新，旧同步记录不能覆盖当前版本")
		}
		return nil
	}
	var parsed string
	if err := json.Unmarshal(*expected, &parsed); err != nil || parsed != priorGeneration {
		return fmt.Errorf("来源已有更新，旧同步记录不能覆盖当前版本")
	}
	return nil
}

func newImportEntry(input ImportInput, id, textHash, generation, revision string) map[string]any {
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
	return entry
}

func (store *Store) Import(input ImportInput) (map[string]any, error) {
	if err := normalizeImportInput(&input); err != nil {
		return nil, err
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
	if unchangedImport(prior, input, textHash) {
		return map[string]any{"entry": view(prior), "unchanged": true}, nil
	}
	if err := checkImportGeneration(prior, input.ExpectedGeneration); err != nil {
		return nil, err
	}
	generation := "gen_" + strings.ReplaceAll(ids.New(), "-", "")
	revision := "rev_" + strings.ReplaceAll(ids.New(), "-", "")
	entry := newImportEntry(input, id, textHash, generation, revision)
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

func containsHan(value string) bool {
	return strings.ContainsFunc(value, func(char rune) bool { return unicode.Is(unicode.Han, char) })
}

func appendTerm(result *[]string, seen map[string]bool, term string) bool {
	if seen[term] {
		return false
	}
	seen[term] = true
	*result = append(*result, term)
	return len(*result) >= 30
}

func chineseCandidates(part string) []string {
	characters := []rune(part)
	candidates := []string{part}
	for _, size := range []int{2, 3} {
		for index := 0; index+size <= len(characters); index++ {
			candidates = append(candidates, string(characters[index:index+size]))
		}
	}
	return candidates
}

func searchTerms(query string) []string { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
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
		if !containsHan(part) {
			if appendTerm(&result, seen, part) {
				break
			}
			continue
		}
		for _, candidate := range chineseCandidates(part) {
			if appendTerm(&result, seen, candidate) {
				break
			}
		}
		if len(result) >= 30 {
			break
		}
	}
	return result
}

type searchCandidate struct{ evidence, source, content string }

func validateSearch(query string, ids []string) error {
	if trimmed := strings.TrimSpace(query); len([]rune(trimmed)) < 1 || len([]rune(trimmed)) > 2000 {
		return fmt.Errorf("搜索内容无效")
	}
	for _, id := range ids {
		if !sourceIDPattern.MatchString(id) {
			return fmt.Errorf("资料标识无效")
		}
	}
	return nil
}

func searchScope(ids []string) (string, []any) {
	if ids == nil {
		return "", nil
	}
	if len(ids) == 0 {
		return " AND 0", nil
	}
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	return " AND source_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

func querySearchCandidates(db *sql.DB, terms, longTerms []string, scope string, args []any) ([]searchCandidate, error) {
	if len(longTerms) == len(terms) {
		matchParts := []string{}
		for _, term := range longTerms {
			matchParts = append(matchParts, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
		queryArgs := append([]any{strings.Join(matchParts, " OR ")}, args...)
		rows, err := db.Query(
			"SELECT evidence_id,source_id,content FROM native_chunks WHERE rowid IN "+
				"(SELECT rowid FROM native_chunks_fts WHERE native_chunks_fts MATCH ?) AND chunk_index>=0"+scope+" LIMIT 500",
			queryArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanSearchCandidates(rows)
	}
	conditions := []string{}
	queryArgs := []any{}
	for _, term := range terms {
		conditions = append(conditions, "instr(lower(content),?)>0")
		queryArgs = append(queryArgs, term)
	}
	queryArgs = append(queryArgs, args...)
	rows, err := db.Query( // NOSONAR: only fixed SQL fragments and '?' placeholders are concatenated; every value is a bound argument.
		"SELECT evidence_id,source_id,content FROM native_chunks WHERE chunk_index>=0 AND ("+
			strings.Join(conditions, " OR ")+")"+scope+" LIMIT 500", queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSearchCandidates(rows)
}

func scanSearchCandidates(rows *sql.Rows) ([]searchCandidate, error) {
	candidates := []searchCandidate{}
	for rows.Next() {
		var row searchCandidate
		if err := rows.Scan(&row.evidence, &row.source, &row.content); err != nil {
			return nil, err
		}
		candidates = append(candidates, row)
	}
	return candidates, rows.Err()
}

func rankSearchCandidates(terms []string, candidates []searchCandidate) {
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
}

func searchHits(store *Store, candidates []searchCandidate) ([]any, error) {
	hits := []any{}
	for _, row := range diverse(candidates, 8, func(value searchCandidate) string { return value.source }) {
		citation, err := store.Citation(row.evidence)
		if err != nil {
			return nil, err
		}
		hits = append(hits, citation)
	}
	return hits, nil
}

func (store *Store) Search(query string, ids []string) (map[string]any, error) {
	if err := validateSearch(query, ids); err != nil {
		return nil, err
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
	scope, args := searchScope(ids)
	candidates, err := querySearchCandidates(store.db, terms, longTerms, scope, args)
	if err != nil {
		return nil, err
	}
	rankSearchCandidates(terms, candidates)
	hits, err := searchHits(store, candidates)
	if err != nil {
		return nil, err
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

func validateFixedOptions(selection map[string]any) error {
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
	return nil
}

func validateStringList(selection map[string]any, key string, limit, maxRunes int) error {
	items, ok := selection[key].([]any)
	if !ok || len(items) > limit {
		return fmt.Errorf("分类选项无效")
	}
	for _, item := range items {
		value, ok := item.(string)
		if !ok || len([]rune(value)) > maxRunes {
			return fmt.Errorf("分类选项无效")
		}
	}
	return nil
}

func validateQualityTier(selection map[string]any) error {
	tier, ok := selection["quality_tier"]
	if !ok {
		return nil
	}
	value, ok := tier.(string)
	if !ok || !map[string]bool{"S": true, "A": true, "B": true, "C": true, "unknown": true}[value] {
		return fmt.Errorf("分类选项无效")
	}
	return nil
}

func validateSelection(selection map[string]any) error {
	for key := range selection {
		if !selectionKeys[key] {
			return fmt.Errorf("分类选项无效")
		}
	}
	if err := validateFixedOptions(selection); err != nil {
		return err
	}
	topic, ok := selection["topic"].(string)
	if !ok || len([]rune(topic)) > 80 {
		return fmt.Errorf("分类选项无效")
	}
	if err := validateStringList(selection, "subtopics", 6, 60); err != nil {
		return err
	}
	if err := validateStringList(selection, "keywords", 16, 64); err != nil {
		return err
	}
	return validateQualityTier(selection)
}

func (store *Store) Stats() (map[string]any, error) {
	list, err := store.List()
	if err != nil {
		return nil, err
	}
	return list["stats"].(map[string]any), nil
}

type VectorStatus struct {
	Model       string `json:"model"`
	Active      bool   `json:"active"`
	Index       string `json:"index"`
	VectorCount int    `json:"vector_count"`
}

type VectorRow struct {
	EvidenceID string
	Vector     []float64
}

func (store *Store) Inventory() ([]StoredChunk, error) {
	return inventoryFrom(store.db)
}

func inventoryFrom(db queryer) ([]StoredChunk, error) {
	rows, err := db.Query(
		"SELECT evidence_id,source_id,generation_id,source_revision_id,chunk_index,content,digest,locator " +
			"FROM native_chunks WHERE chunk_index>=0 ORDER BY source_id,chunk_index")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []StoredChunk{}
	for rows.Next() {
		var row StoredChunk
		if err := rows.Scan(&row.EvidenceID, &row.SourceID, &row.GenerationID, &row.SourceRevisionID,
			&row.ChunkIndex, &row.Content, &row.Digest, &row.Locator); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (store *Store) InventoryDigest() (string, error) {
	return inventoryDigestFrom(store.db)
}

func inventoryDigestFrom(db queryer) (string, error) {
	inventory, err := inventoryFrom(db)
	if err != nil {
		return "", err
	}
	pairs := make([][]string, 0, len(inventory))
	for _, row := range inventory {
		pairs = append(pairs, []string{row.EvidenceID, row.Digest})
	}
	encoded, err := json.Marshal(pairs)
	if err != nil {
		return "", err
	}
	return sha256Hex(string(encoded)), nil
}

func (store *Store) VectorStatus() (VectorStatus, error) {
	status := VectorStatus{Model: "bge-small-zh-v1-5", Index: "not_built"}
	var enabled string
	if err := store.db.QueryRow("SELECT json FROM native_migrations WHERE name='vector-enabled'").Scan(&enabled); err == nil {
		status.Active = strings.TrimSpace(enabled) != "" && strings.TrimSpace(enabled) != "false"
	} else if err != sql.ErrNoRows {
		return VectorStatus{}, err
	}
	var pointer string
	if err := store.db.QueryRow("SELECT json FROM native_migrations WHERE name='vector-index'").Scan(&pointer); err == nil {
		var state struct {
			Digest string `json:"digest"`
			Count  int    `json:"count"`
		}
		if err := json.Unmarshal([]byte(pointer), &state); err == nil {
			digest, err := store.InventoryDigest()
			if err != nil {
				return VectorStatus{}, err
			}
			status.VectorCount = state.Count
			if digest == state.Digest {
				status.Index = "ready"
			} else {
				status.Index = "stale"
			}
		}
	} else if err != sql.ErrNoRows {
		return VectorStatus{}, err
	}
	return status, nil
}

func (store *Store) EnableVectors() (VectorStatus, error) {
	if _, err := store.db.Exec("INSERT OR REPLACE INTO native_migrations VALUES('vector-enabled','true')"); err != nil {
		return VectorStatus{}, err
	}
	return store.VectorStatus()
}

func (store *Store) PublishVectors(expectedDigest string, rows []VectorRow) (VectorStatus, error) {
	transaction, err := store.db.Begin()
	if err != nil {
		return VectorStatus{}, err
	}
	defer func() { _ = transaction.Rollback() }()
	inventory, err := inventoryFrom(transaction)
	if err != nil {
		return VectorStatus{}, err
	}
	digest, err := inventoryDigestFrom(transaction)
	if err != nil {
		return VectorStatus{}, err
	}
	if digest != expectedDigest || len(rows) != len(inventory) {
		return VectorStatus{}, fmt.Errorf("建立索引期间资料发生变化，保留原索引")
	}
	known := map[string]bool{}
	for _, row := range inventory {
		known[row.EvidenceID] = true
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if !known[row.EvidenceID] || seen[row.EvidenceID] || len(row.Vector) != 512 {
			return VectorStatus{}, fmt.Errorf("建立索引期间资料发生变化，保留原索引")
		}
		for _, value := range row.Vector {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return VectorStatus{}, fmt.Errorf("建立索引期间资料发生变化，保留原索引")
			}
		}
		seen[row.EvidenceID] = true
	}
	if _, err := transaction.Exec("DELETE FROM native_vectors"); err != nil {
		return VectorStatus{}, err
	}
	for _, row := range rows {
		encoded, err := json.Marshal(row.Vector)
		if err != nil {
			return VectorStatus{}, err
		}
		if _, err := transaction.Exec("INSERT INTO native_vectors VALUES(?,?)", row.EvidenceID, string(encoded)); err != nil {
			return VectorStatus{}, err
		}
	}
	pointer, err := json.Marshal(map[string]any{"digest": expectedDigest, "count": len(rows)})
	if err != nil {
		return VectorStatus{}, err
	}
	if _, err := transaction.Exec(
		"INSERT OR REPLACE INTO native_migrations VALUES('vector-index',?)", string(pointer),
	); err != nil {
		return VectorStatus{}, err
	}
	if err := transaction.Commit(); err != nil {
		return VectorStatus{}, err
	}
	return store.VectorStatus()
}

type semanticRow struct {
	id     string
	source string
	score  float64
}

type rankRow struct{ id, source string }

func validateVector(vector []float64) error {
	if len(vector) != 512 {
		return fmt.Errorf("查询向量无效")
	}
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("查询向量无效")
		}
	}
	return nil
}

func semanticScope(ids []string) (string, []any) {
	if ids == nil {
		return "", nil
	}
	if len(ids) == 0 {
		return " WHERE 0", nil
	}
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	return " WHERE c.source_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

func scanSemanticRows(rows *sql.Rows, vector []float64) ([]semanticRow, map[string]string, error) {
	defer rows.Close()
	semantic := []semanticRow{}
	sources := map[string]string{}
	for rows.Next() {
		var evidence, encoded, source string
		if err := rows.Scan(&evidence, &encoded, &source); err != nil {
			return nil, nil, err
		}
		var values []float64
		if err := json.Unmarshal([]byte(encoded), &values); err != nil || len(values) != 512 {
			return nil, nil, fmt.Errorf("查询向量无效")
		}
		value := 0.0
		for index := 0; index < 512; index++ {
			value += values[index] * vector[index]
		}
		semantic = append(semantic, semanticRow{id: evidence, source: source, score: value})
		sources[evidence] = source
	}
	return semantic, sources, rows.Err()
}

func rankSemanticRows(semantic []semanticRow) map[string]float64 {
	sort.SliceStable(semantic, func(left, right int) bool { return semantic[left].score > semantic[right].score })
	if len(semantic) > 16 {
		semantic = semantic[:16]
	}
	ranked := map[string]float64{}
	for index, row := range semantic {
		ranked[row.id] = 1 / float64(60+index)
	}
	return ranked
}

func mergeLexicalHits(lexical map[string]any, ranked map[string]float64, sources map[string]string) {
	hits := lexical["hits"].([]any)
	for index, item := range hits {
		hit := item.(map[string]any)
		id := stringValue(hit["evidence_id"])
		ranked[id] += 1 / float64(60+index)
		sources[id] = stringValue(hit["bvid"])
	}
}

func orderedSemanticRows(ranked map[string]float64, sources map[string]string) []rankRow {
	ordered := []rankRow{}
	for id, source := range sources {
		if _, ok := ranked[id]; ok {
			ordered = append(ordered, rankRow{id: id, source: source})
		}
	}
	sort.SliceStable(ordered, func(left, right int) bool { return ranked[ordered[left].id] > ranked[ordered[right].id] })
	return diverse(ordered, 8, func(row rankRow) string { return row.source })
}

func semanticCitations(store *Store, ordered []rankRow) ([]any, error) {
	result := []any{}
	for _, row := range ordered {
		citation, err := store.Citation(row.id)
		if err != nil {
			return nil, err
		}
		result = append(result, citation)
	}
	return result, nil
}

func (store *Store) SemanticSearch(query string, vector []float64, ids []string) (map[string]any, error) {
	lexical, err := store.Search(query, ids)
	if err != nil {
		return nil, err
	}
	status, err := store.VectorStatus()
	if err != nil {
		return nil, err
	}
	if !status.Active || status.Index != "ready" {
		trace := lexical["trace"].(map[string]any)
		trace["vector"] = map[string]any{
			"state": status.Index, "note": "资料或语义索引尚未就绪，本次使用当前关键词结果。",
		}
		return lexical, nil
	}
	if err := validateVector(vector); err != nil {
		return nil, err
	}
	scope, args := semanticScope(ids)
	rows, err := store.db.Query( // NOSONAR: scope only contains fixed fragments and '?' placeholders; every value is a bound argument.
		"SELECT v.evidence_id,v.vector,c.source_id FROM native_vectors v JOIN native_chunks c ON c.evidence_id=v.evidence_id"+scope,
		args...)
	if err != nil {
		return nil, err
	}
	semantic, sources, err := scanSemanticRows(rows, vector)
	if err != nil {
		return nil, err
	}
	ranked := rankSemanticRows(semantic)
	mergeLexicalHits(lexical, ranked, sources)
	result, err := semanticCitations(store, orderedSemanticRows(ranked, sources))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"hits": result,
		"trace": map[string]any{
			"keyword": lexical["trace"].(map[string]any)["keyword"],
			"vector": map[string]any{
				"state": "ready", "note": "本地中文语义与关键词合并检索；优先覆盖不同资料来源，引用已核验。",
			},
		},
	}, nil
}
