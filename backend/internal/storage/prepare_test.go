package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareBacksUpOlderDatabaseOnce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "assistant.db")
	write, err := OpenWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := BootstrapInto(write); err != nil {
		t.Fatal(err)
	}
	if _, err := write.db.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := Prepare(path, filepath.Join(root, "Backups"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Backup == "" || report.FromVersion != 0 || report.ToVersion != SchemaVersion {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(report.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	write, err = OpenWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := BootstrapInto(write); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	report, err = Prepare(path, filepath.Join(root, "Backups"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Backup != "" || report.FromVersion != SchemaVersion {
		t.Fatalf("unexpected repeated prepare: %+v", report)
	}
}

func TestPrepareRejectsNewerDatabase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "assistant.db")
	write, err := OpenWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := BootstrapInto(write); err != nil {
		t.Fatal(err)
	}
	if _, err := write.db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(path, filepath.Join(root, "Backups")); err == nil {
		t.Fatal("newer database accepted")
	}
}

func TestPrepareMissingDatabase(t *testing.T) {
	report, err := Prepare(filepath.Join(t.TempDir(), "missing.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Backup != "" || report.FromVersion != 0 || report.ToVersion != SchemaVersion {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestPrepareRejectsCorruptDatabase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "assistant.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(path, filepath.Join(root, "Backups")); err == nil {
		t.Fatal("corrupt database accepted")
	}
}
