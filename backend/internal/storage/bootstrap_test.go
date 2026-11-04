package storage

import (
	"database/sql"
	"testing"
)

func TestAddColumnIfMissingUsesAllowlist(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("CREATE TABLE lectures(id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := addColumnIfMissing(db, "lectures", "transcript_path"); err != nil {
		t.Fatalf("known column rejected: %v", err)
	}
	if err := addColumnIfMissing(db, "lectures", "transcript_path"); err != nil {
		t.Fatalf("idempotent column add failed: %v", err)
	}
	if err := addColumnIfMissing(db, "lectures", "unknown"); err == nil {
		t.Fatal("unknown column accepted")
	}
	if err := addColumnIfMissing(db, "unknown", "transcript_path"); err == nil {
		t.Fatal("unknown table accepted")
	}
	if _, err := db.Exec("CREATE TABLE correction_checkpoints(lecture_id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := addColumnIfMissing(db, "correction_checkpoints", "compact_version"); err != nil {
		t.Fatalf("checkpoint version column rejected: %v", err)
	}
}
