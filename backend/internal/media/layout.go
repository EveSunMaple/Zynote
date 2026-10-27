package media

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type LayoutResult struct {
	Outputs []string `json:"outputs"`
	Cleanup []string `json:"cleanup"`
}

var layoutNameForbidden = regexp.MustCompile(`[\\/:\x00-\x1f]`)
var layoutNameTrailing = regexp.MustCompile(`[. ]$`)
var layoutDatePattern = regexp.MustCompile(`^\d{4}-(?:0[1-9]|1[0-2])`)

func layoutBase(directory string) (string, error) {
	base, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("目录整理根路径无效")
	}
	info, err := os.Lstat(base)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("目录整理根路径无效")
	}
	return base, nil
}

func layoutFolder(base string, options NativeMediaOptions, created string) (string, []string, error) {
	parts := []string{}
	if strings.Contains(options.FolderLayout, "type") {
		category := "视频"
		if options.SubtitleOnly {
			category = "字幕"
		} else if options.AudioOnly {
			category = "音频"
		}
		parts = append(parts, category)
	}
	if strings.Contains(options.FolderLayout, "date") {
		date := layoutDatePattern.FindString(created)
		if date == "" {
			return "", nil, fmt.Errorf("下载创建月份无效")
		}
		parts = append(parts, date)
	}
	folder := base
	for _, part := range parts {
		folder = filepath.Join(folder, part)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return "", nil, fmt.Errorf("整理目录包含链接")
		}
		dirInfo, err := os.Lstat(folder)
		if err != nil || dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
			return "", nil, fmt.Errorf("整理目录包含链接")
		}
	}
	return folder, parts, nil
}

func publishLayoutFile(base, folder, name string) error {
	if name == "" || layoutNameForbidden.MatchString(name) || strings.HasPrefix(name, ".") ||
		layoutNameTrailing.MatchString(name) ||
		strings.EqualFold(name, "job.json") || strings.EqualFold(name, "job.writing") {
		return fmt.Errorf("待整理成果文件名无效")
	}
	source := filepath.Join(base, name)
	target := filepath.Join(folder, name)
	item, err := os.Lstat(source)
	if err != nil || !item.Mode().IsRegular() || item.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("待整理成果不是普通文件")
	}
	hash, err := MediaFileHash(source)
	if err != nil {
		return fmt.Errorf("待整理成果不是普通文件")
	}
	if copyErr := copyExclusive(source, target); copyErr != nil {
		if !os.IsExist(copyErr) {
			return copyErr
		}
	}
	copied, err := os.Lstat(target)
	if err != nil || !copied.Mode().IsRegular() || copied.Mode()&os.ModeSymlink != 0 || copied.Size() != item.Size() {
		return fmt.Errorf("目录整理成果校验失败，原文件和已有目标均保留")
	}
	copiedHash, err := MediaFileHash(target)
	if err != nil || copiedHash != hash {
		return fmt.Errorf("目录整理成果校验失败，原文件和已有目标均保留")
	}
	sourceHash, err := MediaFileHash(source)
	if err != nil || sourceHash != hash {
		return fmt.Errorf("目录整理成果校验失败，原文件和已有目标均保留")
	}
	return nil
}

func PublishMediaLayout(directory string, names []string, options NativeMediaOptions, created string) (LayoutResult, error) {
	if options.FolderLayout == "source" {
		return LayoutResult{Outputs: names, Cleanup: []string{}}, nil
	}
	base, err := layoutBase(directory)
	if err != nil {
		return LayoutResult{}, err
	}
	folder, parts, err := layoutFolder(base, options, created)
	if err != nil {
		return LayoutResult{}, err
	}
	outputs := []string{}
	for _, name := range names {
		if err := publishLayoutFile(base, folder, name); err != nil {
			return LayoutResult{}, err
		}
		outputs = append(outputs, strings.Join(append(append([]string{}, parts...), name), "/"))
	}
	return LayoutResult{Outputs: outputs, Cleanup: names}, nil
}

func copyExclusive(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("待整理成果不是普通文件")
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(target)
		return fmt.Errorf("目录整理复制失败")
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return fmt.Errorf("目录整理复制失败")
	}
	return output.Close()
}
