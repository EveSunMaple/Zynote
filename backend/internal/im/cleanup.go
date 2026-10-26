package im

import (
	"encoding/json"
	"fmt"
	"time"
)

type CleanupPreview struct {
	Scope     Scope   `json:"scope"`
	Hash      string  `json:"hash"`
	Count     int     `json:"count"`
	Bytes     int     `json:"bytes"`
	CreatedAt float64 `json:"createdAt"`
	Done      bool    `json:"done"`
}

func (service *Service) Cleanup(input map[string]any) (any, error) {
	scope, _, err := service.Store.CheckScope(input)
	if err != nil {
		return nil, err
	}
	if _, err := service.Store.db.Exec("CREATE TABLE IF NOT EXISTS native_im_cleanup(id TEXT PRIMARY KEY,json TEXT NOT NULL)"); err != nil {
		return nil, err
	}
	previewID := text(input["preview_id"])
	confirmed, _ := input["user_confirmed"].(bool)
	if previewID == "" {
		protected, err := service.cleanupProtected(scope)
		if err != nil {
			return nil, err
		}
		rows := []Message{}
		reason := "只清理本软件保存的所选会话文字记录。"
		if protected {
			reason = "未完成回复待办或未确认发送仍需这些消息，当前保留。"
		} else {
			rows, err = service.cleanupRows(scope)
			if err != nil {
				return nil, err
			}
		}
		bytes := 0
		for _, row := range rows {
			bytes += len([]byte(row.Text))
		}
		preview := CleanupPreview{Scope: scope, Hash: hashValue(rows), Count: len(rows), Bytes: bytes, CreatedAt: float64(time.Now().UnixMilli())}
		id := newID()
		encoded, _ := json.Marshal(preview)
		if _, err := service.Store.db.Exec("INSERT INTO native_im_cleanup VALUES(?,?)", id, string(encoded)); err != nil {
			return nil, err
		}
		return map[string]any{"preview_id": id, "count": preview.Count, "images": 0, "bytes": preview.Bytes, "reason": reason}, nil
	}
	if !idPattern.MatchString(previewID) || !confirmed {
		return nil, fmt.Errorf("请先确认清理预览")
	}
	var raw string
	if err := service.Store.db.QueryRow("SELECT json FROM native_im_cleanup WHERE id=?", previewID).Scan(&raw); err != nil {
		return nil, fmt.Errorf("清理预览不存在")
	}
	preview := CleanupPreview{}
	if json.Unmarshal([]byte(raw), &preview) != nil {
		return nil, fmt.Errorf("清理预览无效")
	}
	encodedScope, _ := json.Marshal(preview.Scope)
	currentScope, _ := json.Marshal(scope)
	if string(encodedScope) != string(currentScope) {
		return nil, fmt.Errorf("清理预览不属于当前账号和会话")
	}
	if preview.Done {
		return map[string]any{"deleted": preview.Count, "replayed": true}, nil
	}
	if time.Now().UnixMilli()-int64(preview.CreatedAt) > 10*60*1000 {
		return nil, fmt.Errorf("清理预览已过期或出现待办，请重新检查")
	}
	protected, err := service.cleanupProtected(scope)
	if err != nil {
		return nil, err
	}
	if protected {
		return nil, fmt.Errorf("清理预览已过期或出现待办，请重新检查")
	}
	rows, err := service.cleanupRows(scope)
	if err != nil {
		return nil, err
	}
	if hashValue(rows) != preview.Hash || len(rows) != preview.Count {
		return nil, fmt.Errorf("预览之后消息已改变，当前未删除")
	}
	tx, err := service.Store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec("DELETE FROM messages WHERE platform=? AND account=? AND login_epoch=? AND peer=?",
		scope.Platform, scope.Account, scope.LoginEpoch, scope.Peer)
	if err != nil {
		return nil, err
	}
	deleted, _ := result.RowsAffected()
	if int(deleted) != preview.Count {
		return nil, fmt.Errorf("清理数量与预览不符，修改已回滚")
	}
	preview.Done = true
	encoded, _ := json.Marshal(preview)
	if _, err := tx.Exec("UPDATE native_im_cleanup SET json=? WHERE id=?", string(encoded), previewID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": preview.Count, "replayed": false}, nil
}

func (service *Service) cleanupRows(scope Scope) ([]Message, error) {
	rows, err := service.Store.db.Query("SELECT mid,ts,sender,text,kind FROM messages WHERE platform=? AND account=? AND login_epoch=? AND peer=? ORDER BY mid LIMIT 10001",
		scope.Platform, scope.Account, scope.LoginEpoch, scope.Peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.MID, &message.TS, &message.Sender, &message.Text, &message.Kind); err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	if len(result) > 10000 {
		return nil, fmt.Errorf("本会话记录较多，需要分段清理，当前未删除记录")
	}
	return result, rows.Err()
}

func (service *Service) cleanupProtected(scope Scope) (bool, error) {
	rows, err := service.Store.db.Query("SELECT scope FROM send_operations WHERE state NOT IN ('sent','completed','failed_before_request')")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		var value Scope
		if json.Unmarshal([]byte(raw), &value) != nil {
			return true, nil
		}
		if value == scope {
			return true, nil
		}
	}
	var exists int
	_ = service.Store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='native_im_state'").Scan(&exists)
	if exists > 0 {
		rows, err := service.Store.db.Query("SELECT json FROM native_im_state WHERE kind='job'")
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return false, err
			}
			var value map[string]any
			if json.Unmarshal([]byte(raw), &value) != nil {
				return true, nil
			}
			if text(value["state"]) != "completed" {
				encoded, _ := json.Marshal(value["scope"])
				var jobScope Scope
				if json.Unmarshal(encoded, &jobScope) == nil && jobScope == scope {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
