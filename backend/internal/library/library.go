package library

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

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
