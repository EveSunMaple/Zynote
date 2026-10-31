package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func writeTestFile(t *testing.T, path string, content ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	value := "test"
	if len(content) > 0 {
		value = content[0]
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func entryNames(entries []map[string]any) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry["name"].(string))
	}
	return names
}

func TestListDirectorySortsDirectoriesFirst(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "b-file.txt"))
	writeTestFile(t, filepath.Join(root, "A-file.txt"))
	writeTestFile(t, filepath.Join(root, "zeta", "inner.txt"))
	writeTestFile(t, filepath.Join(root, "Alpha", "inner.txt"))
	entries, err := ListDirectory(root, "")
	if err != nil {
		t.Fatalf("list root: %v", err)
	}
	names := entryNames(entries)
	want := []string{"Alpha", "zeta", "A-file.txt", "b-file.txt"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	if entries[0]["type"] != "dir" || entries[2]["type"] != "file" {
		t.Fatalf("unexpected types: %+v", entries)
	}
}

func TestListDirectoryNested(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "PPT", "微积分", "第1讲.pptx"))
	entries, err := ListDirectory(root, "PPT")
	if err != nil {
		t.Fatalf("list PPT: %v", err)
	}
	if len(entries) != 1 || entries[0]["path"] != "PPT/微积分" || entries[0]["type"] != "dir" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	entries, err = ListDirectory(root, "PPT/微积分")
	if err != nil {
		t.Fatalf("list course: %v", err)
	}
	if len(entries) != 1 || entries[0]["path"] != "PPT/微积分/第1讲.pptx" || entries[0]["type"] != "file" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestListDirectoryRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"../outside", "PPT/../../outside", filepath.Join(root, "absolute")} {
		if _, err := ListDirectory(root, path); err == nil {
			t.Fatalf("path %q should be rejected", path)
		} else {
			var appErr *contract.AppError
			if !errors.As(err, &appErr) || appErr.Code != contract.CodeInputInvalid {
				t.Fatalf("path %q error = %v, want INPUT_INVALID", path, err)
			}
		}
	}
}

func TestListDirectoryMissingDirectory(t *testing.T) {
	root := t.TempDir()
	if _, err := ListDirectory(root, "missing"); err == nil {
		t.Fatal("missing directory should fail")
	} else {
		var appErr *contract.AppError
		if !errors.As(err, &appErr) || appErr.Code != contract.CodeNotFound {
			t.Fatalf("error = %v, want NOT_FOUND", err)
		}
	}
}

func TestMigrateLegacyDirectories(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "知识库数据", "knowledge-native.sqlite"), "db")
	writeTestFile(t, filepath.Join(root, "Knowledge", "existing.txt"), "existing")
	writeTestFile(t, filepath.Join(root, "视频提取临时任务", "task", "video.mp4"), "video")
	if err := os.MkdirAll(filepath.Join(root, "字幕同步记录"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "笔记生成记录"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "PPT", "课程", "第1讲.pptx"), "ppt")
	if err := os.MkdirAll(filepath.Join(root, "PPT", "测试"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyDirectories(root); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, target := range []string{"Knowledge/knowledge-native.sqlite", "Knowledge/existing.txt", "VideoTasks/task/video.mp4"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(target))); err != nil {
			t.Fatalf("missing %s: %v", target, err)
		}
	}
	for _, legacy := range []string{"知识库数据", "字幕同步记录", "笔记生成记录", "视频提取临时任务"} {
		if _, err := os.Stat(filepath.Join(root, legacy)); !os.IsNotExist(err) {
			t.Fatalf("legacy directory %s should be gone", legacy)
		}
	}
	CleanupEmptyCourseDirectories(root)
	if _, err := os.Stat(filepath.Join(root, "PPT", "测试")); !os.IsNotExist(err) {
		t.Fatal("empty course directory should be removed")
	}
	for _, kept := range []string{"PPT", "PPT/课程", "PPT/课程/第1讲.pptx"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(kept))); err != nil {
			t.Fatalf("kept path %s missing: %v", kept, err)
		}
	}
}
