package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

// SchemaVersion is stored in SQLite user_version. Increment it whenever a
// migration needs a pre-upgrade backup.
const SchemaVersion = 1

type PrepareReport struct {
	Backup      string `json:"backup,omitempty"`
	FromVersion int    `json:"fromVersion"`
	ToVersion   int    `json:"toVersion"`
}

func backupName(version int) string {
	return fmt.Sprintf("assistant-v%d-%s.db", version, time.Now().UTC().Format("20060102-150405.000"))
}

// Prepare checks an existing database before the versioned bootstrap runs.
// A database from an older schema is copied to backupDir before any write.
func Prepare(path, backupDir string) (PrepareReport, error) {
	report := PrepareReport{ToVersion: SchemaVersion}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, contract.Unavailable("数据库路径不可访问")
	}
	if info.IsDir() || info.Size() == 0 {
		return report, contract.Unavailable("数据库文件无效")
	}
	db, err := openReadOnly(path)
	if err != nil {
		return report, contract.Unavailable("数据库无法打开")
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		db.Close()
		return report, contract.Unavailable("数据库完整性检查失败")
	}
	if integrity != "ok" {
		db.Close()
		return report, contract.Unavailable("数据库完整性异常，请从备份恢复")
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&report.FromVersion); err != nil {
		db.Close()
		return report, contract.Unavailable("数据库版本读取失败")
	}
	db.Close()
	if report.FromVersion > SchemaVersion {
		return report, contract.Unavailable("数据版本高于当前应用，请升级应用后再试")
	}
	if report.FromVersion == SchemaVersion {
		return report, nil
	}
	if backupDir == "" {
		backupDir = filepath.Dir(path)
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return report, contract.Unavailable("数据库备份目录不可用")
	}
	target := filepath.Join(backupDir, backupName(report.FromVersion))
	backup, err := Backup(path, target)
	if err != nil {
		return report, err
	}
	report.Backup = backup.Path
	return report, nil
}
