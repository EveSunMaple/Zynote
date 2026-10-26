package im

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var platforms = map[string]bool{"qq_napcat": true, "weixin_local": true, "weixin_web": true, "weixin_ilink": true}

type Crypt func(data []byte, decrypt bool) ([]byte, error)

type Scope struct {
	ConnectionID string `json:"connection_id"`
	Revision     string `json:"revision"`
	Platform     string `json:"platform"`
	Account      string `json:"account"`
	LoginEpoch   string `json:"login_epoch"`
	Peer         string `json:"peer"`
}

type Message struct {
	MID    string  `json:"mid"`
	TS     float64 `json:"ts"`
	Sender string  `json:"sender"`
	Text   string  `json:"text"`
	Kind   string  `json:"kind"`
}

type Connection struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Platform       string            `json:"platform"`
	Revision       string            `json:"revision"`
	Fields         map[string]string `json:"fields"`
	HasCredentials bool              `json:"has_credentials"`
	HasSession     bool              `json:"has_session"`
	Account        string            `json:"account"`
	LoginEpoch     string            `json:"login_epoch"`
	CheckedAt      any               `json:"checked_at"`
	Capabilities   any               `json:"capabilities"`
	OnlineState    string            `json:"online_state"`
}

type PrivateConnection struct {
	Connection
	Secrets  map[string]string `json:"secrets"`
	Session  any               `json:"session"`
	Snapshot string            `json:"snapshot"`
}

type Store struct {
	db    *sql.DB
	root  string
	Crypt Crypt
	mu    sync.Mutex
}

func newID() string { return strings.ReplaceAll(ids.New(), "-", "") }

func Open(root string, crypt Crypt) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("通讯资料根目录无效")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("通讯资料根目录无效")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.EqualFold(resolved, root) {
		return nil, fmt.Errorf("通讯资料根目录无效")
	}
	path := filepath.Join(root, "im-connections.sqlite3")
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("通讯数据库路径无效")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("通讯数据库不可用")
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA busy_timeout = 5000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("通讯数据库不可用")
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS registry(id INTEGER PRIMARY KEY CHECK(id=1),revision TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS connections(id TEXT PRIMARY KEY,name TEXT NOT NULL,platform TEXT NOT NULL,revision TEXT NOT NULL,public TEXT NOT NULL,sealed BLOB NOT NULL,account TEXT NOT NULL DEFAULT '',login_epoch TEXT NOT NULL DEFAULT '',session BLOB,checked_at REAL,capabilities TEXT NOT NULL DEFAULT '{}');
		CREATE TABLE IF NOT EXISTS messages(platform TEXT,account TEXT,login_epoch TEXT,peer TEXT,mid TEXT,ts REAL,sender TEXT,text TEXT,kind TEXT,PRIMARY KEY(platform,account,login_epoch,peer,mid));
		CREATE TABLE IF NOT EXISTS send_operations(operation_id TEXT PRIMARY KEY,digest TEXT NOT NULL,scope TEXT NOT NULL,state TEXT NOT NULL,receipt TEXT NOT NULL,updated REAL NOT NULL);
		CREATE TABLE IF NOT EXISTS login_operations(operation_id TEXT PRIMARY KEY,scope TEXT NOT NULL,projection TEXT NOT NULL);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("通讯数据库不可用")
	}
	if _, err := db.Exec("INSERT OR IGNORE INTO registry VALUES(1,?)", newID()); err != nil {
		db.Close()
		return nil, fmt.Errorf("通讯数据库不可用")
	}
	return &Store{db: db, root: root, Crypt: crypt}, nil
}

func (store *Store) Close() error { return store.db.Close() }

func hashValue(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (store *Store) revision() string {
	var value string
	_ = store.db.QueryRow("SELECT revision FROM registry WHERE id=1").Scan(&value)
	return value
}

func decodeJSON(value string, fallback any) any {
	if value == "" {
		return fallback
	}
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) != nil || parsed == nil {
		return fallback
	}
	return parsed
}

func (store *Store) public(row map[string]any) Connection {
	fields := map[string]string{}
	if encoded, ok := row["public"].(string); ok {
		_ = json.Unmarshal([]byte(encoded), &fields)
	}
	sealed, _ := row["sealed"].([]byte)
	session, _ := row["session"].([]byte)
	checked := row["checked_at"]
	state := "unverified"
	if checked != nil {
		state = "last_verified"
	}
	return Connection{
		ID: text(row["id"]), Name: text(row["name"]), Platform: text(row["platform"]), Revision: text(row["revision"]),
		Fields: fields, HasCredentials: len(sealed) > 0, HasSession: len(session) > 0,
		Account: text(row["account"]), LoginEpoch: text(row["login_epoch"]), CheckedAt: checked,
		Capabilities: decodeJSON(text(row["capabilities"]), map[string]any{}), OnlineState: state,
	}
}

func (store *Store) Listing() (map[string]any, error) {
	rows, err := store.db.Query("SELECT * FROM connections ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []Connection{}
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		connections = append(connections, store.public(row))
	}
	return map[string]any{"revision": store.revision(), "connections": connections}, rows.Err()
}

func scanRow(rows *sql.Rows) (map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}
	row := map[string]any{}
	for index, column := range columns {
		row[column] = values[index]
	}
	return row, nil
}

func (store *Store) readConnection(id string) (map[string]any, error) {
	rows, err := store.db.Query("SELECT * FROM connections WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("通讯连接不存在")
	}
	return scanRow(rows)
}

func (store *Store) Capture(key, revision string) (Connection, error) {
	if !idPattern.MatchString(key) {
		return Connection{}, fmt.Errorf("通讯连接标识无效")
	}
	row, err := store.readConnection(key)
	if err != nil {
		return Connection{}, err
	}
	value := store.public(row)
	if revision != "" && value.Revision != revision {
		return Connection{}, fmt.Errorf("通讯连接已改变，请刷新")
	}
	return value, nil
}

func (store *Store) privateSnapshot(key, revision string) (string, error) {
	if _, err := store.Capture(key, revision); err != nil {
		return "", err
	}
	row, err := store.readConnection(key)
	if err != nil {
		return "", err
	}
	sealed, _ := row["sealed"].([]byte)
	session, _ := row["session"].([]byte)
	return hashValue([]string{base64.StdEncoding.EncodeToString(sealed), base64.StdEncoding.EncodeToString(session)}), nil
}

func (store *Store) PrivateState(key, revision string) (PrivateConnection, error) {
	snapshot, err := store.privateSnapshot(key, revision)
	if err != nil {
		return PrivateConnection{}, err
	}
	row, err := store.readConnection(key)
	if err != nil {
		return PrivateConnection{}, err
	}
	value := store.public(row)
	secrets := map[string]string{}
	sealed, _ := row["sealed"].([]byte)
	if len(sealed) > 0 {
		if store.Crypt == nil {
			return PrivateConnection{}, fmt.Errorf("系统加密桥不可用")
		}
		raw, err := store.Crypt(sealed, true)
		if err != nil {
			return PrivateConnection{}, err
		}
		_ = json.Unmarshal(raw, &secrets)
	}
	var session any
	if encrypted, _ := row["session"].([]byte); len(encrypted) > 0 {
		if store.Crypt == nil {
			return PrivateConnection{}, fmt.Errorf("系统加密桥不可用")
		}
		raw, err := store.Crypt(encrypted, true)
		if err != nil {
			return PrivateConnection{}, err
		}
		_ = json.Unmarshal(raw, &session)
	}
	current, err := store.privateSnapshot(key, revision)
	if err != nil || current != snapshot {
		return PrivateConnection{}, fmt.Errorf("通讯凭据或会话已改变，请重新读取")
	}
	return PrivateConnection{Connection: value, Secrets: secrets, Session: session, Snapshot: snapshot}, nil
}

func stringMap(input any) map[string]string {
	values, _ := input.(map[string]any)
	result := map[string]string{}
	for key, value := range values {
		if textValue, ok := value.(string); ok {
			result[key] = textValue
		}
	}
	return result
}

func (store *Store) Change(input map[string]any) (any, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	action := text(input["action"])
	expected := text(input["expected_revision"])
	if !idPattern.MatchString(expected) || (action != "create" && action != "update" && action != "delete") {
		return nil, fmt.Errorf("通讯连接修改参数无效")
	}
	if store.revision() != expected {
		return nil, fmt.Errorf("通讯连接列表已改变，请刷新")
	}
	var old *Connection
	if action != "create" {
		value, err := store.Capture(text(input["id"]), "")
		if err != nil {
			return nil, err
		}
		old = &value
	}
	name := strings.TrimSpace(text(input["name"]))
	platform := text(input["platform"])
	fields := stringMap(input["fields"])
	secrets := stringMap(input["secrets"])
	sealed := []byte{}
	unchanged := false
	if action != "delete" {
		if name == "" || len([]rune(name)) > 80 || !platforms[platform] || (old != nil && platform != old.Platform) {
			return nil, fmt.Errorf("连接名称或平台无效")
		}
		for key := range fields {
			if key != "mode" && key != "webui_url" && key != "onebot_url" {
				return nil, fmt.Errorf("通讯连接字段无效")
			}
		}
		for key := range secrets {
			if key != "webui_token" && key != "onebot_token" {
				return nil, fmt.Errorf("通讯连接字段无效")
			}
		}
		if platform == "qq_napcat" {
			mode := fields["mode"]
			if mode == "" {
				mode = "managed"
			}
			if mode == "managed" {
				fields = map[string]string{"mode": "managed"}
				secrets = map[string]string{}
			} else if mode == "manual" {
				origin, err := Loopback(fields["webui_url"])
				if err != nil {
					return nil, err
				}
				fields = map[string]string{"mode": "manual", "webui_url": origin}
				if fields["onebot_url"] != "" {
					onebot, err := Loopback(fields["onebot_url"])
					if err != nil {
						return nil, err
					}
					fields["onebot_url"] = onebot
				}
			} else {
				return nil, fmt.Errorf("QQ 连接模式无效")
			}
		} else if len(fields) > 0 || len(secrets) > 0 {
			return nil, fmt.Errorf("微信连接不预填第三方凭据")
		}
		if old != nil && fmt.Sprint(old.Fields) == fmt.Sprint(fields) && len(secrets) == 0 {
			unchanged = true
		} else if old != nil && fmt.Sprint(old.Fields) == fmt.Sprint(fields) {
			prior, err := store.PrivateState(old.ID, old.Revision)
			if err != nil {
				return nil, err
			}
			merged := prior.Secrets
			for key, value := range secrets {
				merged[key] = value
			}
			if len(merged) > 0 {
				if store.Crypt == nil {
					return nil, fmt.Errorf("系统加密桥不可用")
				}
				raw, _ := json.Marshal(merged)
				sealed, err = store.Crypt(raw, false)
				if err != nil {
					return nil, err
				}
			}
			unchanged = true
			for key, value := range secrets {
				if prior.Secrets[key] != value {
					unchanged = false
				}
			}
		} else if len(secrets) > 0 {
			if store.Crypt == nil {
				return nil, fmt.Errorf("系统加密桥不可用")
			}
			raw, _ := json.Marshal(secrets)
			var err error
			sealed, err = store.Crypt(raw, false)
			if err != nil {
				return nil, err
			}
		}
	}
	if store.revision() != expected {
		return nil, fmt.Errorf("通讯连接列表已改变，请刷新")
	}
	tx, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if action == "delete" {
		if old == nil {
			return nil, fmt.Errorf("通讯连接不存在")
		}
		if _, err := tx.Exec("DELETE FROM connections WHERE id=?", old.ID); err != nil {
			return nil, err
		}
	} else {
		public, _ := json.Marshal(fields)
		if old == nil {
			var count int
			_ = tx.QueryRow("SELECT COUNT(*) FROM connections").Scan(&count)
			if count >= 20 {
				return nil, fmt.Errorf("最多保存20个通讯连接")
			}
			if _, err := tx.Exec("INSERT INTO connections(id,name,platform,revision,public,sealed) VALUES(?,?,?,?,?,?)",
				newID(), name, platform, newID(), string(public), sealed); err != nil {
				return nil, err
			}
		} else if unchanged && len(secrets) == 0 {
			if _, err := tx.Exec("UPDATE connections SET name=?,revision=? WHERE id=?", name, newID(), old.ID); err != nil {
				return nil, err
			}
		} else {
			if _, err := tx.Exec("UPDATE connections SET name=?,revision=?,public=?,sealed=?,account='',login_epoch='',session=NULL,checked_at=NULL,capabilities='{}' WHERE id=?",
				name, newID(), string(public), sealed, old.ID); err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.Exec("UPDATE registry SET revision=? WHERE id=1", newID()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return store.Listing()
}

func (store *Store) Bind(key, revision, account string, session any, capabilities any, beforeCommit func() error) (Connection, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !idPattern.MatchString(key) || account == "" || len([]rune(account)) > 200 {
		return Connection{}, fmt.Errorf("通讯绑定参数无效")
	}
	if _, err := store.Capture(key, revision); err != nil {
		return Connection{}, err
	}
	if store.Crypt == nil {
		return Connection{}, fmt.Errorf("系统加密桥不可用")
	}
	raw, _ := json.Marshal(session)
	encrypted, err := store.Crypt(raw, false)
	if err != nil {
		return Connection{}, err
	}
	capabilitiesJSON, _ := json.Marshal(capabilities)
	current, err := store.Capture(key, revision)
	if err != nil {
		return Connection{}, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback()
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return Connection{}, err
		}
	}
	if current.Account != "" && current.Account != account {
		return Connection{}, fmt.Errorf("账号与此连接原绑定不一致，请新建连接")
	}
	if _, err := tx.Exec("UPDATE connections SET revision=?,account=?,login_epoch=?,session=?,checked_at=?,capabilities=? WHERE id=?",
		newID(), account, newID(), encrypted, float64(time.Now().UnixMilli())/1000, string(capabilitiesJSON), key); err != nil {
		return Connection{}, err
	}
	if _, err := tx.Exec("UPDATE registry SET revision=? WHERE id=1", newID()); err != nil {
		return Connection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Connection{}, err
	}
	return store.Capture(key, "")
}

func parseScope(input any) (Scope, error) {
	var scope Scope
	switch value := input.(type) {
	case Scope:
		scope = value
	case *Scope:
		if value != nil {
			scope = *value
		}
	default:
		row, _ := input.(map[string]any)
		scope = Scope{
			ConnectionID: text(row["connection_id"]), Revision: text(row["revision"]),
			Platform: text(row["platform"]), Account: text(row["account"]),
			LoginEpoch: text(row["login_epoch"]), Peer: text(row["peer"]),
		}
	}
	if !idPattern.MatchString(scope.ConnectionID) || !idPattern.MatchString(scope.Revision) ||
		!idPattern.MatchString(scope.LoginEpoch) || !platforms[scope.Platform] ||
		scope.Account == "" || len([]rune(scope.Account)) > 200 ||
		scope.Peer == "" || len([]rune(scope.Peer)) > 200 {
		return Scope{}, fmt.Errorf("通讯会话参数无效")
	}
	return scope, nil
}

func (store *Store) CheckScope(input any) (Scope, Connection, error) {
	scope, err := parseScope(input)
	if err != nil {
		return Scope{}, Connection{}, err
	}
	row, err := store.Capture(scope.ConnectionID, scope.Revision)
	if err != nil {
		return Scope{}, Connection{}, err
	}
	if row.Platform != scope.Platform || row.Account != scope.Account ||
		row.LoginEpoch != scope.LoginEpoch || !row.HasSession {
		return Scope{}, Connection{}, fmt.Errorf("消息账号或登录代际已改变")
	}
	return scope, row, nil
}

type Limits struct {
	ReadEnabled   bool `json:"read_enabled"`
	Archive       bool `json:"archive_enabled"`
	MaxMessages   int  `json:"max_messages"`
	MaxBytes      int  `json:"max_bytes"`
	RetentionDays int  `json:"retention_days"`
}

func (store *Store) AccountLimits(input any) (Scope, Limits, error) {
	scope, _, err := store.CheckScope(input)
	if err != nil {
		return Scope{}, Limits{}, err
	}
	limits := Limits{ReadEnabled: true, Archive: true, MaxMessages: 2000, MaxBytes: 20 * 1048576, RetentionDays: 30}
	var exists int
	_ = store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='im_rules'").Scan(&exists)
	if exists > 0 {
		var raw string
		if store.db.QueryRow("SELECT value FROM im_rules WHERE platform=? AND account=? AND peer=''", scope.Platform, scope.Account).Scan(&raw) == nil {
			var value map[string]any
			if json.Unmarshal([]byte(raw), &value) == nil {
				if item, ok := value["read_enabled"].(bool); ok {
					limits.ReadEnabled = item
				}
				if item, ok := value["archive_enabled"].(bool); ok {
					limits.Archive = item
				}
				if item := int(number(value["max_messages"])); item >= 50 && item <= 100000 {
					limits.MaxMessages = item
				}
				if item := int(number(value["max_bytes"])); item >= 1048576 && item <= 1073741824 {
					limits.MaxBytes = item
				}
				if item := int(number(value["retention_days"])); item >= 1 && item <= 3650 {
					limits.RetentionDays = item
				}
			}
		}
	}
	return scope, limits, nil
}

func (store *Store) RequireRead(input any) error {
	_, limits, err := store.AccountLimits(input)
	if err != nil {
		return err
	}
	if !limits.ReadEnabled {
		return fmt.Errorf("此账号已停用消息读取，旧权限设置继续生效")
	}
	return nil
}

func (store *Store) SaveMessages(input any, messages []Message) (map[string]any, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	scope, limits, err := store.AccountLimits(input)
	if err != nil {
		return nil, err
	}
	return store.saveMessagesLocked(scope, messages, limits)
}

func (store *Store) saveMessagesLocked(scope Scope, messages []Message, limits Limits) (map[string]any, error) {
	if len(messages) > 5000 {
		return nil, fmt.Errorf("本次消息数量超过上限")
	}
	if !limits.ReadEnabled {
		return nil, fmt.Errorf("此账号已停用消息读取，旧权限设置继续生效")
	}
	if !limits.Archive {
		return nil, fmt.Errorf("此账号已停用消息保存，未保存正文或推进游标")
	}
	tx, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, message := range messages {
		var ts float64
		var sender, textValue, kind string
		err := tx.QueryRow("SELECT ts,sender,text,kind FROM messages WHERE platform=? AND account=? AND login_epoch=? AND peer=? AND mid=?",
			scope.Platform, scope.Account, scope.LoginEpoch, scope.Peer, message.MID).Scan(&ts, &sender, &textValue, &kind)
		if err == nil && hashValue(message) != hashValue(Message{MID: message.MID, TS: ts, Sender: sender, Text: textValue, Kind: kind}) {
			return nil, fmt.Errorf("同一消息编号内容已改变，旧记录保留")
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO messages VALUES(?,?,?,?,?,?,?,?,?)",
			scope.Platform, scope.Account, scope.LoginEpoch, scope.Peer, message.MID, message.TS, message.Sender, message.Text, message.Kind); err != nil {
			return nil, err
		}
	}
	var count, bytes int64
	if err := tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(length(CAST(text AS BLOB))),0) FROM messages WHERE platform=? AND account=?",
		scope.Platform, scope.Account).Scan(&count, &bytes); err != nil {
		return nil, err
	}
	var mediaBytes int64
	var exists int
	_ = tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='im_media'").Scan(&exists)
	if exists > 0 {
		_ = tx.QueryRow("SELECT COALESCE(SUM(length(bytes)),0) FROM im_media WHERE platform=? AND account=?",
			scope.Platform, scope.Account).Scan(&mediaBytes)
	}
	if count > int64(limits.MaxMessages) || bytes+mediaBytes > int64(limits.MaxBytes) {
		return nil, fmt.Errorf("账号保存记录达到容量上限，请先预览清理；本次正文与游标未保存")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"saved": len(messages), "coverage": "仅此次接口可取得的消息，不代表全部历史"}, nil
}

func (store *Store) SaveSessionAndMessages(input any, session any, messages []Message, snapshot string, beforeCommit func() error) (map[string]any, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	scope, limits, err := store.AccountLimits(input)
	if err != nil {
		return nil, err
	}
	current, err := store.privateSnapshot(scope.ConnectionID, scope.Revision)
	if err != nil || current != snapshot {
		return nil, fmt.Errorf("通讯凭据或会话已改变，请重新读取")
	}
	if store.Crypt == nil {
		return nil, fmt.Errorf("系统加密桥不可用")
	}
	raw, _ := json.Marshal(session)
	encrypted, err := store.Crypt(raw, false)
	if err != nil {
		return nil, err
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return nil, err
		}
	}
	result, err := store.saveMessagesLocked(scope, messages, limits)
	if err != nil {
		return nil, err
	}
	if _, err := store.db.Exec("UPDATE connections SET session=?,checked_at=? WHERE id=? AND revision=?",
		encrypted, float64(time.Now().UnixMilli())/1000, scope.ConnectionID, scope.Revision); err != nil {
		return nil, err
	}
	return result, nil
}

func (store *Store) SavedDirectory(input any) (map[string]any, error) {
	scope, _, err := store.CheckScope(input)
	if err != nil {
		return nil, err
	}
	if err := store.RequireRead(input); err != nil {
		return nil, err
	}
	rows, err := store.db.Query("SELECT peer,COUNT(*) FROM messages WHERE platform=? AND account=? AND login_epoch=? GROUP BY peer ORDER BY MAX(ts) DESC,peer LIMIT 500",
		scope.Platform, scope.Account, scope.LoginEpoch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	peers := []map[string]any{}
	for rows.Next() {
		var peer string
		var count int
		if err := rows.Scan(&peer, &count); err != nil {
			return nil, err
		}
		peers = append(peers, map[string]any{"peer": peer, "name": peer, "count": count})
	}
	return map[string]any{"peers": peers, "coverage": "当前账号登录代际最近 500 个已保存会话"}, rows.Err()
}

func (store *Store) ReadMessages(input any) (map[string]any, error) {
	scope, _, err := store.CheckScope(input)
	if err != nil {
		return nil, err
	}
	if err := store.RequireRead(input); err != nil {
		return nil, err
	}
	value, _ := input.(map[string]any)
	limit := int(number(value["limit"]))
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("消息条数无效")
	}
	cursor := ""
	if row, ok := value["cursor"].(map[string]any); ok {
		cursor, _ = row["mid"].(string)
	}
	statement := "SELECT mid,ts,sender,text,kind FROM messages WHERE platform=? AND account=? AND login_epoch=? AND peer=?"
	arguments := []any{scope.Platform, scope.Account, scope.LoginEpoch, scope.Peer}
	if cursor != "" {
		ts := number(value["cursor"].(map[string]any)["ts"])
		statement += " AND (ts<? OR (ts=? AND mid<?))"
		arguments = append(arguments, ts, ts, cursor)
	}
	statement += " ORDER BY ts DESC,mid DESC LIMIT ?"
	arguments = append(arguments, limit+1)
	rows, err := store.db.Query(statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.MID, &message.TS, &message.Sender, &message.Text, &message.Kind); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	var next any
	if len(messages) > limit {
		last := messages[limit-1]
		next = map[string]any{"ts": last.TS, "mid": last.MID}
		messages = messages[:limit]
	}
	return map[string]any{"messages": messages, "next_cursor": next}, rows.Err()
}

type sendEntry struct {
	Digest  string  `json:"digest"`
	Scope   Scope   `json:"scope"`
	State   string  `json:"state"`
	Receipt any     `json:"receipt"`
	Updated float64 `json:"updated"`
}

func (store *Store) ClaimSend(input any) (bool, map[string]any, error) {
	scope, _, err := store.CheckScope(input)
	if err != nil {
		return false, nil, err
	}
	value, _ := input.(map[string]any)
	op := text(value["operation_id"])
	message := strings.TrimSpace(text(value["text"]))
	if !idPattern.MatchString(op) || message == "" || len([]rune(message)) > 4000 || value["user_confirmed"] != true {
		return false, nil, fmt.Errorf("发送参数无效")
	}
	digest := hashValue([]any{scope, message})
	store.mu.Lock()
	defer store.mu.Unlock()
	var priorDigest, receipt string
	err = store.db.QueryRow("SELECT digest,receipt FROM send_operations WHERE operation_id=?", op).Scan(&priorDigest, &receipt)
	if err == nil {
		if priorDigest != digest {
			return false, nil, fmt.Errorf("发送编号已用于不同内容")
		}
		var parsed map[string]any
		_ = json.Unmarshal([]byte(receipt), &parsed)
		return false, parsed, nil
	}
	if err != sql.ErrNoRows {
		return false, nil, err
	}
	receiptValue := map[string]any{"operation_id": op, "state": "sending"}
	encodedScope, _ := json.Marshal(scope)
	encodedReceipt, _ := json.Marshal(receiptValue)
	if _, err := store.db.Exec("INSERT INTO send_operations VALUES(?,?,?,?,?,?)",
		op, digest, string(encodedScope), "sending", string(encodedReceipt), float64(time.Now().UnixMilli())/1000); err != nil {
		return false, nil, err
	}
	return true, receiptValue, nil
}

func (store *Store) FinishSend(operationID, state, messageID string) (map[string]any, error) {
	if !idPattern.MatchString(operationID) || (state != "sent" && state != "unknown") || len([]rune(messageID)) > 200 {
		return nil, fmt.Errorf("发送回执参数无效")
	}
	receipt := map[string]any{"operation_id": operationID, "state": state}
	if messageID != "" {
		receipt["message_id"] = messageID
	}
	encoded, _ := json.Marshal(receipt)
	result, err := store.db.Exec("UPDATE send_operations SET state=?,receipt=?,updated=? WHERE operation_id=? AND state='sending'",
		state, string(encoded), float64(time.Now().UnixMilli())/1000, operationID)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, fmt.Errorf("发送回执状态已改变，需要人工核对")
	}
	return receipt, nil
}

func (store *Store) SendStatus(input any) (map[string]any, error) {
	scope, _, err := store.CheckScope(input)
	if err != nil {
		return nil, err
	}
	value, _ := input.(map[string]any)
	op := text(value["operation_id"])
	if !idPattern.MatchString(op) {
		return nil, fmt.Errorf("发送回执编号无效")
	}
	var encodedScope, state, receipt string
	if err := store.db.QueryRow("SELECT scope,state,receipt FROM send_operations WHERE operation_id=?", op).Scan(&encodedScope, &state, &receipt); err != nil {
		return nil, fmt.Errorf("此发送回执不属于当前账号和会话")
	}
	encodedCurrent, _ := json.Marshal(scope)
	if encodedScope != string(encodedCurrent) {
		return nil, fmt.Errorf("此发送回执不属于当前账号和会话")
	}
	result := map[string]any{}
	_ = json.Unmarshal([]byte(receipt), &result)
	if state == "sending" {
		state = "unknown"
	}
	result["state"] = state
	return result, nil
}

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	}
	return 0
}

func sortMessages(messages []Message) {
	sort.SliceStable(messages, func(left, right int) bool { return messages[left].TS > messages[right].TS })
}
