package media

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func CurrentMediaPlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func MediaToolName(value string) (string, error) {
	if value != "ffmpeg" && value != "ffprobe" {
		return "", fmt.Errorf("媒体工具名称无效")
	}
	return value, nil
}

func MediaExecutablePath(resources, tool, platform string) (string, error) {
	if _, err := MediaToolName(tool); err != nil {
		return "", err
	}
	if !filepath.IsAbs(resources) {
		return "", fmt.Errorf("媒体资源目录必须是绝对路径")
	}
	if platform != "win32" && platform != "darwin" && platform != "linux" {
		return "", fmt.Errorf("此平台尚未提供媒体执行组件")
	}
	name := tool
	if platform == "win32" {
		name += ".exe"
	}
	return filepath.Join(resources, "video", name), nil
}

var fallbackDirectories = map[string][]string{
	"darwin": {"/opt/homebrew/bin", "/usr/local/bin"},
	"linux":  {"/usr/bin", "/usr/local/bin"},
}

var fallbackRoots = map[string][]string{
	"darwin": {"/opt/homebrew/", "/usr/local/"},
	"linux":  {"/usr/"},
}

func executableRegular(path string, platform string) (os.FileInfo, error) { // NOSONAR: path handling matches the platform API contract.
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("媒体执行文件必须是普通文件")
	}
	if platform != "win32" && info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("媒体组件不可执行或无访问权限")
	}
	return info, nil
}

func systemExecutableCandidate(candidate string, roots []string) (string, bool) {
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	target, err := filepath.EvalSymlinks(candidate)
	if err != nil || filepath.Base(target) != filepath.Base(candidate) {
		return "", false
	}
	inside := false
	for _, root := range roots {
		if strings.HasPrefix(target, root) {
			inside = true
			break
		}
	}
	if !inside {
		return "", false
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm()&0o111 == 0 {
		return "", false
	}
	return target, true
}

func checkedSystemExecutable(tool, platform string) (string, error) {
	directories, ok := fallbackDirectories[platform]
	roots, rootsOK := fallbackRoots[platform]
	if !ok || !rootsOK {
		return "", fmt.Errorf("未找到媒体处理组件")
	}
	for _, directory := range directories {
		if target, found := systemExecutableCandidate(filepath.Join(directory, tool), roots); found {
			return target, nil
		}
	}
	return "", fmt.Errorf("未找到媒体处理组件")
}

func checkExecutableParents(path string) error {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("媒体执行目录包含链接或无效路径")
		}
		if filepath.Dir(parent) == parent {
			return nil
		}
	}
}

func checkExecutableInside(resources, path, platform string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(resources)
	if err != nil {
		return "", err
	}
	compare := func(value string) string {
		if platform == "win32" {
			return strings.ToLower(value)
		}
		return value
	}
	inside, err := filepath.Rel(base, target)
	if err != nil {
		return "", err
	}
	if compare(target) != compare(path) || inside == "" || inside == ".." ||
		strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", fmt.Errorf("媒体执行文件超出资源目录或路径已改变")
	}
	return target, nil
}

func mediaExecutableError(err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("未找到媒体处理组件")
	}
	if os.IsPermission(err) {
		return fmt.Errorf("媒体组件不可执行或无访问权限")
	}
	return err
}

func CheckedMediaExecutable(resources, tool, platform string, allowSystemFallback bool) (string, error) {
	path, err := MediaExecutablePath(resources, tool, platform)
	if err != nil {
		return "", err
	}
	_, err = executableRegular(path, platform)
	if err == nil {
		if parentErr := checkExecutableParents(path); parentErr != nil {
			err = parentErr
		} else {
			target, insideErr := checkExecutableInside(resources, path, platform)
			if insideErr == nil {
				return target, nil
			}
			err = insideErr
		}
	}
	if allowSystemFallback && os.IsNotExist(err) {
		return checkedSystemExecutable(tool, platform)
	}
	return "", mediaExecutableError(err)
}
