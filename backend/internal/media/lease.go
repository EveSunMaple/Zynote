package media

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"
)

type DownloadQueueLease struct {
	db *sql.DB
	tx *sql.Tx
}

func AcquireDownloadQueue(root string, ownerName string) (*DownloadQueueLease, error) { // NOSONAR: path handling matches the platform API contract.
	if ownerName != "native-download-owner.sqlite" && ownerName != "native-research-owner.sqlite" {
		return nil, fmt.Errorf("下载进程目录无效")
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	directory := root
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("下载进程目录无效")
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("下载进程目录无效")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || strings.ToLower(resolved) != strings.ToLower(directory) {
		return nil, fmt.Errorf("下载进程目录无效")
	}
	path := filepath.Join(directory, ownerName)
	if file, err := os.Lstat(path); err == nil {
		if !file.Mode().IsRegular() || file.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("下载进程所有权文件无效")
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return nil, fmt.Errorf("下载进程目录无效")
	}
	if _, err := db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		db.Close()
		return nil, fmt.Errorf("下载进程目录无效")
	}
	if _, err := db.Exec("PRAGMA busy_timeout=0"); err != nil {
		db.Close()
		return nil, fmt.Errorf("下载进程目录无效")
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("此下载目录已由另一个进程使用，请先关闭原进程")
	}
	fail := func() (*DownloadQueueLease, error) {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("此下载目录已由另一个进程使用，请先关闭原进程")
	}
	if _, err := tx.Exec("CREATE TABLE IF NOT EXISTS native_owner (pid INTEGER NOT NULL)"); err != nil {
		return fail()
	}
	if _, err := tx.Exec("DELETE FROM native_owner"); err != nil {
		return fail()
	}
	if _, err := tx.Exec("INSERT INTO native_owner(pid) VALUES (?)", os.Getpid()); err != nil {
		return fail()
	}
	return &DownloadQueueLease{db: db, tx: tx}, nil
}

func (lease *DownloadQueueLease) Close() {
	if lease.tx != nil {
		lease.tx.Rollback()
		lease.tx = nil
	}
	if lease.db != nil {
		lease.db.Close()
		lease.db = nil
	}
}
