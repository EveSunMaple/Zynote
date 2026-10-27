package library

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const errLibraryPathInvalid = "不能操作文件夹、链接或资料目录以外的文件"

func ManagedFile(root, path string) (string, error) {
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", contract.Unavailable("资料目录不可访问")
	}
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", contract.InputInvalid("只能管理资料文件夹中的文件")
	}
	_, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", contract.InputInvalid(errLibraryPathInvalid)
	}
	targetReal, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", contract.InputInvalid(errLibraryPathInvalid)
	}
	relative, err := filepath.Rel(base, targetReal)
	if err != nil || relative == "" || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return "", contract.InputInvalid("只能管理资料文件夹中的文件")
	}
	actualRelative, err := filepath.Rel(base, targetReal)
	if err != nil || strings.HasPrefix(actualRelative, "..") || filepath.IsAbs(actualRelative) ||
		info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", contract.InputInvalid(errLibraryPathInvalid)
	}
	return target, nil
}

func CourseDirectory(root, category, course string) (string, error) {
	directory := filepath.Join(root, category)
	if course != "" {
		directory = filepath.Join(directory, FolderName(course))
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", contract.Unavailable("资料目录不可创建")
	}
	return directory, nil
}
