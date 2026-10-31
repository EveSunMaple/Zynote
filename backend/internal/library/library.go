package library

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

var legacyDirectoryNames = map[string]string{
	"字幕同步记录":   "SubtitleSync",
	"画面识别成果":   "VisualOutputs",
	"知识库数据":    "Knowledge",
	"笔记生成记录":   "NoteGeneration",
	"视频提取临时任务": "VideoTasks",
}

// MigrateLegacyDirectories moves legacy Chinese data directories to their
// English names. Empty legacy directories are removed instead of renamed, so
// old unused folders do not linger in the working directory.
func MigrateLegacyDirectories(root string) error {
	rootInfo, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return contract.Unavailable("资料目录不可访问")
	}
	if !rootInfo.IsDir() {
		return contract.Unavailable("资料目录无效")
	}
	for legacy, target := range legacyDirectoryNames {
		source := filepath.Join(root, legacy)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return contract.Unavailable("资料目录不可访问")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return contract.Unavailable("资料目录不可访问")
		}
		if len(entries) == 0 {
			if err := os.Remove(source); err != nil {
				return contract.Unavailable("旧资料目录无法删除")
			}
			continue
		}
		targetPath := filepath.Join(root, target)
		if _, err := os.Lstat(targetPath); os.IsNotExist(err) {
			if err := os.Rename(source, targetPath); err != nil {
				return contract.Unavailable("资料目录迁移失败")
			}
			continue
		} else if err != nil {
			return contract.Unavailable("资料目录不可访问")
		}
		if err := mergeLegacyDirectory(source, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func mergeLegacyDirectory(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return contract.Unavailable("资料目录不可访问")
	}
	for _, entry := range entries {
		from := filepath.Join(source, entry.Name())
		to := filepath.Join(target, entry.Name())
		if _, err := os.Lstat(to); err == nil {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				if err := os.MkdirAll(to, 0o755); err != nil {
					return contract.Unavailable("资料目录迁移失败")
				}
				if err := mergeLegacyDirectory(from, to); err != nil {
					return err
				}
			}
			continue
		} else if !os.IsNotExist(err) {
			return contract.Unavailable("资料目录不可访问")
		}
		if err := os.Rename(from, to); err != nil {
			return contract.Unavailable("资料目录迁移失败")
		}
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
		// Conflicting entries stay in the legacy directory for manual recovery.
		slog.Warn("legacy library directory still has conflicts", "path", source)
	}
	return nil
}

// CleanupEmptyCourseDirectories removes empty subdirectories under the four
// course material categories. Category roots and non-empty folders are kept.
func CleanupEmptyCourseDirectories(root string) {
	for _, category := range Categories {
		base := filepath.Join(root, category)
		info, err := os.Lstat(base)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		pruneEmptyDirectories(base, true)
	}
}

func pruneEmptyDirectories(dir string, keep bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			pruneEmptyDirectories(filepath.Join(dir, entry.Name()), false)
		}
	}
	if keep {
		return
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

var Categories = []string{"PPT", "Transcripts", "Notes", "PastExams"}

var reservedDevice = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)`)
var invalidChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func FolderName(name string) string {
	value := invalidChars.ReplaceAllString(name, "_")
	value = strings.TrimRight(value, ". ")
	if utf8.RuneCountInString(value) > 100 {
		value = string([]rune(value)[:100])
	}
	value = strings.TrimRight(value, ". ")
	if value == "" {
		value = "未命名课程"
	}
	if reservedDevice.MatchString(value) {
		return "_" + value
	}
	return value
}

type walkEntry struct {
	Path     string
	Name     string
	Category string
	Course   string
	Size     int64
	Modified string
}

type directoryEntry struct {
	Path     string
	Name     string
	Type     string
	Size     int64
	Modified string
}

// ListDirectory returns the direct children of a directory under the library
// root. The path is relative to root and slash-separated; empty means root.
func ListDirectory(root, relative string) ([]map[string]any, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, contract.Unavailable("资料目录不存在")
		}
		return nil, contract.Unavailable("资料目录不可访问")
	}
	if !rootInfo.IsDir() {
		return nil, contract.Unavailable("资料目录无效")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." {
		clean = ""
	}
	if clean != "" && !filepath.IsLocal(clean) {
		return nil, contract.InputInvalid("目录路径无效")
	}
	target := root
	if clean != "" {
		target = filepath.Join(root, clean)
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, contract.NewError(contract.CodeNotFound, "目录不存在")
		}
		return nil, contract.Unavailable("目录不可访问")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, contract.InputInvalid("目录路径无效")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, contract.Unavailable("目录不可访问")
	}
	collected := make([]directoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.ToSlash(filepath.Join(clean, entry.Name()))
		modified := entryInfo.ModTime().UTC().Format(time.RFC3339Nano)
		if entryInfo.IsDir() {
			collected = append(collected, directoryEntry{Path: path, Name: entry.Name(), Type: "dir", Modified: modified})
			continue
		}
		if !entryInfo.Mode().IsRegular() {
			continue
		}
		collected = append(collected, directoryEntry{Path: path, Name: entry.Name(), Type: "file", Size: entryInfo.Size(), Modified: modified})
	}
	sort.Slice(collected, func(i, j int) bool {
		iDir, jDir := collected[i].Type == "dir", collected[j].Type == "dir"
		if iDir != jDir {
			return iDir
		}
		left, right := strings.ToLower(collected[i].Name), strings.ToLower(collected[j].Name)
		if left != right {
			return left < right
		}
		return collected[i].Name < collected[j].Name
	})
	result := make([]map[string]any, 0, len(collected))
	for _, entry := range collected {
		result = append(result, map[string]any{
			"path":     entry.Path,
			"name":     entry.Name,
			"type":     entry.Type,
			"size":     entry.Size,
			"modified": entry.Modified,
		})
	}
	return result, nil
}

func ListLibrary(root string) ([]map[string]any, error) {
	var collected []walkEntry
	for _, category := range Categories {
		base := filepath.Join(root, category)
		info, err := os.Lstat(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, contract.Unavailable("资料目录不可访问: " + category)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		if err := walkLibrary(root, base, category, "", &collected); err != nil {
			return nil, err
		}
	}
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].Modified > collected[j].Modified
	})
	result := []map[string]any{}
	for _, entry := range collected {
		result = append(result, map[string]any{
			"path":     entry.Path,
			"name":     entry.Name,
			"category": entry.Category,
			"course":   entry.Course,
			"size":     entry.Size,
			"modified": entry.Modified,
		})
	}
	return result, nil
}

func walkLibrary(root, dir, category, course string, collected *[]walkEntry) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return contract.Unavailable("资料目录不可访问: " + dir)
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			inner := course
			if inner == "" {
				inner = entry.Name()
			}
			if err := walkLibrary(root, path, category, inner, collected); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		*collected = append(*collected, walkEntry{
			Path:     filepath.ToSlash(relative),
			Name:     entry.Name(),
			Category: category,
			Course:   course,
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	return nil
}

func ReserveFile(directory, filename string) (string, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", contract.Unavailable("资料目录不可创建")
	}
	extension := filepath.Ext(filename)
	stem := FolderName(strings.TrimSuffix(filepath.Base(filename), extension))
	for index := 1; ; index++ {
		name := stem + extension
		if index > 1 {
			name = stem + " (" + strconv.Itoa(index) + ")" + extension
		}
		path := filepath.Join(directory, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			file.Close()
			return path, nil
		}
		if !os.IsExist(err) {
			return "", contract.Unavailable("资料文件不可创建")
		}
	}
}
