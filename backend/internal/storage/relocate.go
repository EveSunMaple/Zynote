package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
)

func relocatePath(previousRoot, libraryRoot, value string) (string, bool) {
	if value == "" {
		return value, false
	}
	previous, err := filepath.Abs(previousRoot)
	if err != nil {
		return value, false
	}
	relative, err := filepath.Rel(previous, value)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return value, false
	}
	target := filepath.Join(libraryRoot, relative)
	if _, err := os.Stat(target); err != nil {
		return value, false
	}
	return target, true
}

func relocateCourseFiles(tx *sql.Tx, previousRoot, libraryRoot string) error {
	rows, err := tx.Query("SELECT id,path FROM course_files")
	if err != nil {
		return err
	}
	defer rows.Close()
	type update struct{ id, path string }
	var changes []update
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return err
		}
		if next, changed := relocatePath(previousRoot, libraryRoot, path); changed {
			changes = append(changes, update{id, next})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, change := range changes {
		if _, err := tx.Exec("UPDATE course_files SET path=? WHERE id=?", change.path, change.id); err != nil {
			return err
		}
	}
	return nil
}

func relocateLectures(tx *sql.Tx, previousRoot, libraryRoot string) error {
	rows, err := tx.Query("SELECT id,ppt_path,transcript_path,source_video_path FROM lectures")
	if err != nil {
		return err
	}
	defer rows.Close()
	type lecture struct{ id, ppt, transcript, source string }
	var changes []lecture
	for rows.Next() {
		var row lecture
		if err := rows.Scan(&row.id, &row.ppt, &row.transcript, &row.source); err != nil {
			return err
		}
		ppt, pptChanged := relocatePath(previousRoot, libraryRoot, row.ppt)
		transcript, transcriptChanged := relocatePath(previousRoot, libraryRoot, row.transcript)
		source, sourceChanged := relocatePath(previousRoot, libraryRoot, row.source)
		row.ppt, row.transcript, row.source = ppt, transcript, source
		if pptChanged || transcriptChanged || sourceChanged {
			changes = append(changes, row)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, row := range changes {
		if _, err := tx.Exec(
			"UPDATE lectures SET ppt_path=?,transcript_path=?,source_video_path=? WHERE id=?",
			row.ppt, row.transcript, row.source, row.id,
		); err != nil {
			return err
		}
	}
	return nil
}

func hasLibraryTables(db *sql.DB) (bool, error) {
	var tables int
	err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('course_files','lectures')",
	).Scan(&tables)
	return tables == 2, err
}

// RelocateLibraryPaths updates stored course-file and lecture paths after the
// library directory itself has been moved. Missing targets keep their old path.
func RelocateLibraryPaths(dbPath, previousRoot, libraryRoot string) error {
	if previousRoot == "" || libraryRoot == "" || previousRoot == libraryRoot {
		return nil
	}
	write, err := OpenWrite(dbPath)
	if err != nil {
		return err
	}
	defer write.Close()
	ready, err := hasLibraryTables(write.db)
	if err != nil || !ready {
		return err
	}
	tx, err := write.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := relocateCourseFiles(tx, previousRoot, libraryRoot); err != nil {
		return err
	}
	if err := relocateLectures(tx, previousRoot, libraryRoot); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return write.Checkpoint()
}
