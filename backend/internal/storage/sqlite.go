package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"
)

type HealthReport struct {
	OK          bool           `json:"ok"`
	Integrity   string         `json:"integrity"`
	Exists      bool           `json:"exists"`
	Tables      map[string]int `json:"tables"`
	PageSize    int64          `json:"pageSize"`
	PageCount   int64          `json:"pageCount"`
	WALMode     bool           `json:"walMode"`
	FKIntegrity string         `json:"foreignKeys"`
	Error       string         `json:"error,omitempty"`
}

type BackupReport struct {
	Path    string  `json:"path"`
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`
}

func openReadOnly(path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func Health(path string) (HealthReport, error) {
	report := HealthReport{Tables: map[string]int{}}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			report.Error = "数据库不存在"
			return report, nil
		}
		return report, contract.Unavailable("数据库路径不可访问")
	}
	if info.IsDir() || info.Size() == 0 {
		report.Error = "数据库文件无效"
		return report, nil
	}
	report.Exists = true
	db, err := openReadOnly(path)
	if err != nil {
		report.Error = "数据库无法打开: " + err.Error()
		return report, nil
	}
	defer db.Close()
	integrity := ""
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		report.Error = "完整性检查失败: " + err.Error()
		return report, nil
	}
	report.Integrity = integrity
	report.OK = integrity == "ok"
	if !report.OK {
		report.Error = "数据库完整性异常: " + integrity
		return report, nil
	}
	_ = db.QueryRow("PRAGMA page_size").Scan(&report.PageSize)
	_ = db.QueryRow("PRAGMA page_count").Scan(&report.PageCount)
	var journal string
	_ = db.QueryRow("PRAGMA journal_mode").Scan(&journal)
	report.WALMode = strings.EqualFold(journal, "wal")
	var fk string
	_ = db.QueryRow("PRAGMA foreign_key_check LIMIT 1").Scan(&fk)
	report.FKIntegrity = "ok"
	for _, table := range []string{"courses", "lectures", "messages", "chat_sessions", "course_files"} {
		var count int
		var row *sql.Row
		switch table {
		case "courses":
			row = db.QueryRow("SELECT COUNT(*) FROM courses")
		case "lectures":
			row = db.QueryRow("SELECT COUNT(*) FROM lectures")
		case "messages":
			row = db.QueryRow("SELECT COUNT(*) FROM messages")
		case "chat_sessions":
			row = db.QueryRow("SELECT COUNT(*) FROM chat_sessions")
		case "course_files":
			row = db.QueryRow("SELECT COUNT(*) FROM course_files")
		default:
			continue
		}
		if err := row.Scan(&count); err != nil {
			if strings.Contains(err.Error(), "no such table") {
				report.Tables[table] = -1
				continue
			}
			report.Tables[table] = -1
			continue
		}
		report.Tables[table] = count
	}
	return report, nil
}

func Backup(source, target string) (BackupReport, error) {
	report := BackupReport{}
	if strings.TrimSpace(target) == "" {
		target = source + ".backup-" + time.Now().UTC().Format("20060102-150405")
	}
	if !filepath.IsAbs(target) {
		return report, contract.InputInvalid("备份目标必须为绝对路径")
	}
	if _, err := os.Stat(target); err == nil {
		return report, contract.InputInvalid("备份目标已存在")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return report, contract.Unavailable("备份目录不可用")
	}
	db, err := openReadOnly(source)
	if err != nil {
		return report, fmt.Errorf("数据库无法打开: %w", err)
	}
	defer db.Close()
	started := time.Now()
	if _, err := db.Exec("VACUUM INTO ?", target); err != nil {
		return report, fmt.Errorf("备份失败: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return report, fmt.Errorf("备份文件不可读: %w", err)
	}
	report.Path = target
	report.Bytes = info.Size()
	report.Seconds = time.Since(started).Seconds()
	return report, nil
}
