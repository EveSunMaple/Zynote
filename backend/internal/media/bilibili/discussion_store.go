package bilibili

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DiscussionStore struct {
	rootID string
	path   string
}

func NewDiscussionStore(mediaRoot string) (*DiscussionStore, error) {
	root, err := filepath.Abs(filepath.Join(mediaRoot, "knowledge_base"))
	if err != nil {
		return nil, fmt.Errorf("讨论证据目录无效")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	} else if !os.IsNotExist(resolveErr) {
		return nil, resolveErr
	}
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	digest, err := DiscussionDigest([]any{"kbr", root})
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(mediaRoot, "cache", "video-discussion")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("讨论证据目录不可用")
	}
	return &DiscussionStore{rootID: "kbr_" + digest[:32], path: filepath.Join(directory, "evidence.sqlite3")}, nil
}

func (store *DiscussionStore) connection() (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(store.path)+"?_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA busy_timeout = 10000", "PRAGMA synchronous = FULL"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS snapshots (ref TEXT PRIMARY KEY, root TEXT NOT NULL, bvid TEXT NOT NULL, cid INTEGER NOT NULL, page INTEGER NOT NULL, payload TEXT NOT NULL); CREATE TABLE IF NOT EXISTS reviews (root TEXT,bvid TEXT,cid INTEGER,page INTEGER,item TEXT,digest TEXT,payload TEXT,PRIMARY KEY(root,bvid,cid,page,item)); CREATE TABLE IF NOT EXISTS heads (root TEXT,bvid TEXT,cid INTEGER,page INTEGER,ref TEXT,PRIMARY KEY(root,bvid,cid,page));"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func cloneDiscussionPack(input DiscussionPack) (DiscussionPack, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return DiscussionPack{}, fmt.Errorf("讨论证据数据无效")
	}
	var result DiscussionPack
	if err := json.Unmarshal(encoded, &result); err != nil {
		return DiscussionPack{}, fmt.Errorf("讨论证据数据无效")
	}
	return result, nil
}

func (store *DiscussionStore) Save(input DiscussionPack) (*DiscussionPack, error) {
	pack, err := cloneDiscussionPack(input)
	if err != nil {
		return nil, err
	}
	pack.Ref = ""
	db, err := store.connection()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	reviews, err := tx.Query(
		"SELECT item,digest,payload FROM reviews WHERE root=? AND bvid=? AND cid=? AND page=?",
		store.rootID, pack.Bvid, pack.Cid, pack.Page,
	)
	if err != nil {
		return nil, err
	}
	type reviewRow struct{ item, digest, payload string }
	rows := []reviewRow{}
	for reviews.Next() {
		var row reviewRow
		if err := reviews.Scan(&row.item, &row.digest, &row.payload); err != nil {
			reviews.Close()
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := reviews.Err(); err != nil {
		reviews.Close()
		return nil, err
	}
	reviews.Close()
	for index := range pack.Items {
		item := &pack.Items[index]
		for _, review := range rows {
			if review.item != item.ID || review.digest != item.TextSHA256 {
				continue
			}
			var payload struct {
				Selected bool     `json:"selected"`
				Labels   []string `json:"labels"`
				Note     string   `json:"note"`
				Reviewed bool     `json:"reviewed"`
			}
			if err := json.Unmarshal([]byte(review.payload), &payload); err != nil {
				return nil, err
			}
			item.Selected = payload.Selected
			item.Labels = payload.Labels
			item.Note = payload.Note
			item.Reviewed = payload.Reviewed
			break
		}
		if !item.Reviewed {
			item.Selected = false
		}
	}
	payload, err := CanonicalJSON(&pack)
	if err != nil {
		return nil, err
	}
	if len(payload) > 4*1024*1024 {
		return nil, fmt.Errorf("讨论证据超过保存上限")
	}
	ref := discussionPackRef(store.rootID, payload)
	if _, err := tx.Exec(
		"INSERT OR IGNORE INTO snapshots VALUES(?,?,?,?,?,?)",
		ref, store.rootID, pack.Bvid, pack.Cid, pack.Page, payload,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO heads VALUES(?,?,?,?,?)",
		store.rootID, pack.Bvid, pack.Cid, pack.Page, ref,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	pack.Ref = ref
	return &pack, nil
}

func ParseDiscussionChanges(input json.RawMessage) ([]DiscussionChange, error) {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	changes := []DiscussionChange{}
	if len(input) == 0 {
		return nil, fmt.Errorf("讨论选择无效")
	}
	if err := decoder.Decode(&changes); err != nil {
		return nil, fmt.Errorf("讨论选择无效")
	}
	if len(changes) > discussionChangeLimit {
		return nil, fmt.Errorf("讨论选择无效")
	}
	for _, change := range changes {
		if _, ok := discussionLabels[change.Label]; !ok || len([]rune(change.Note)) > 500 {
			return nil, fmt.Errorf("讨论选择无效")
		}
	}
	return changes, nil
}

func (store *DiscussionStore) Select(ref string, changes []DiscussionChange) (DiscussionPack, string, error) {
	if !discussionRefPattern.MatchString(ref) {
		return DiscussionPack{}, "", fmt.Errorf("讨论记录编号无效")
	}
	db, err := store.connection()
	if err != nil {
		return DiscussionPack{}, "", err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return DiscussionPack{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	var payload string
	if err := tx.QueryRow("SELECT payload FROM snapshots WHERE ref=? AND root=?", ref, store.rootID).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return DiscussionPack{}, "", fmt.Errorf("讨论来源记录不存在或已改变")
		}
		return DiscussionPack{}, "", err
	}
	if discussionPackRef(store.rootID, payload) != ref {
		return DiscussionPack{}, "", fmt.Errorf("讨论来源记录不存在或已改变")
	}
	var pack DiscussionPack
	if err := json.Unmarshal([]byte(payload), &pack); err != nil {
		return DiscussionPack{}, "", err
	}
	var current string
	if err := tx.QueryRow(
		"SELECT ref FROM heads WHERE root=? AND bvid=? AND cid=? AND page=?",
		store.rootID, pack.Bvid, pack.Cid, pack.Page,
	).Scan(&current); err != nil {
		return DiscussionPack{}, "", err
	}
	if current != ref {
		return DiscussionPack{}, "", fmt.Errorf("讨论列表已有更新，请重新读取")
	}
	seen := map[string]bool{}
	for _, change := range changes {
		found := -1
		for index := range pack.Items {
			if pack.Items[index].ID == change.ID {
				found = index
				break
			}
		}
		if found < 0 || seen[change.ID] {
			return DiscussionPack{}, "", fmt.Errorf("讨论选择无效")
		}
		seen[change.ID] = true
		item := &pack.Items[found]
		item.Selected = change.Selected
		item.Labels = []string{change.Label}
		item.Note = change.Note
		item.Reviewed = true
	}
	text, err := renderDiscussion(&pack)
	if err != nil {
		return DiscussionPack{}, "", err
	}
	if text == "" {
		return DiscussionPack{}, "", fmt.Errorf("请至少选择一条讨论证据")
	}
	for id := range seen {
		var item *DiscussionItem
		for index := range pack.Items {
			if pack.Items[index].ID == id {
				item = &pack.Items[index]
				break
			}
		}
		if item == nil {
			continue
		}
		review, err := CanonicalJSON(map[string]any{
			"selected": item.Selected, "labels": item.Labels, "note": item.Note, "reviewed": item.Reviewed,
		})
		if err != nil {
			return DiscussionPack{}, "", err
		}
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO reviews VALUES(?,?,?,?,?,?,?)",
			store.rootID, pack.Bvid, pack.Cid, pack.Page, id, item.TextSHA256, review,
		); err != nil {
			return DiscussionPack{}, "", err
		}
	}
	pack.Ref = ""
	reviewedAt := time.Now().Unix()
	pack.ReviewedAt = &reviewedAt
	newPayload, err := CanonicalJSON(&pack)
	if err != nil {
		return DiscussionPack{}, "", err
	}
	newRef := discussionPackRef(store.rootID, newPayload)
	if _, err := tx.Exec(
		"INSERT OR IGNORE INTO snapshots VALUES(?,?,?,?,?,?)",
		newRef, store.rootID, pack.Bvid, pack.Cid, pack.Page, newPayload,
	); err != nil {
		return DiscussionPack{}, "", err
	}
	if _, err := tx.Exec(
		"UPDATE heads SET ref=? WHERE root=? AND bvid=? AND cid=? AND page=?",
		newRef, store.rootID, pack.Bvid, pack.Cid, pack.Page,
	); err != nil {
		return DiscussionPack{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return DiscussionPack{}, "", err
	}
	pack.Ref = newRef
	return pack, text, nil
}
