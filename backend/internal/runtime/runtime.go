package runtime

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	errRuntimeIntegrity = "组件文件类型、摘要、大小或权限校验失败"
	errRuntimeManifest  = "v2 组件清单格式或应用版本不符"
)

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode,omitempty"`
}

type Component struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Directory string `json:"directory"`
	Files     []File `json:"files"`
	Bytes     int64  `json:"bytes"`
	Target    Target `json:"target"`
}

type Manifest struct {
	Schema         int         `json:"schema"`
	ProductVersion string      `json:"productVersion"`
	Target         Target      `json:"target"`
	Components     []Component `json:"components"`
}

type Target struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
}

type StatusComponent struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Bytes       int64  `json:"bytes"`
	Installed   bool   `json:"installed"`
	Validation  string `json:"validation,omitempty"`
	Installable bool   `json:"installable,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type Status struct {
	Version    string            `json:"version"`
	Validation string            `json:"validation,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Components []StatusComponent `json:"components"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var safePathPattern = regexp.MustCompile(`^[^\\:<>"|?*\x00-\x1f\x7f]+$`)

func targetPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	}
	return runtime.GOOS
}

func targetArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

func relativePath(value string) bool {
	if value == "" || len(value) > 500 || !safePathPattern.MatchString(value) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

func contained(root, relative string) (string, error) {
	if !relativePath(relative) {
		return "", fmt.Errorf("组件路径无效")
	}
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("组件目录无效")
	}
	target := filepath.Join(resolvedRoot, filepath.FromSlash(relative))
	if !strings.HasPrefix(target, resolvedRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("组件路径越界")
	}
	return target, nil
}

func readManifest(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16*1024*1024 {
		return Manifest{}, fmt.Errorf("组件清单无法读取或格式无效")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("组件清单无法读取或格式无效")
	}
	var manifest Manifest
	if json.Unmarshal(raw, &manifest) != nil {
		return Manifest{}, fmt.Errorf("组件清单无法读取或格式无效")
	}
	return manifest, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checkRegular(root, relative string, size int64, digest, mode string) (bool, string) {
	target, err := contained(root, relative)
	if err != nil {
		return false, errRuntimeIntegrity
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "组件文件缺失；v2 安装尚未开放"
		}
		return false, errRuntimeIntegrity
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != size {
		return false, errRuntimeIntegrity
	}
	if mode != "" && runtime.GOOS != "windows" {
		expected := os.FileMode(0o644)
		if mode == "0755" {
			expected = 0o755
		}
		if info.Mode().Perm() != expected {
			return false, errRuntimeIntegrity
		}
	}
	actual, err := hashFile(target)
	if err != nil || actual != digest {
		return false, errRuntimeIntegrity
	}
	return true, ""
}

func invalidStatus(version, reason string) Status {
	return Status{Version: version, Validation: "invalid", Reason: reason, Components: []StatusComponent{}}
}

func legacyComponentInstalled(resourcesRoot string, component Component) bool {
	if len(component.Files) == 0 {
		return false
	}
	for _, item := range component.Files {
		target, err := contained(filepath.Join(resourcesRoot, component.Directory), item.Path)
		if err != nil {
			return false
		}
		if _, err := os.Lstat(target); err != nil {
			return false
		}
	}
	return true
}

func legacyStatus(resourcesRoot, version string, manifest Manifest) Status {
	status := Status{Version: version, Validation: "legacy", Components: []StatusComponent{}}
	for _, component := range manifest.Components {
		status.Components = append(status.Components, StatusComponent{
			ID: component.ID, Label: component.Label, Bytes: component.Bytes,
			Installed: legacyComponentInstalled(resourcesRoot, component), Validation: "legacy",
		})
	}
	return status
}

func CheckStatus(resourcesRoot, version string) Status {
	manifest, err := readManifest(filepath.Join(resourcesRoot, "runtime-manifest.json"))
	if err != nil {
		return invalidStatus(version, err.Error())
	}
	if manifest.Schema == 2 {
		return statusV2(resourcesRoot, version, manifest)
	}
	if manifest.Schema != 1 {
		return invalidStatus(version, "组件清单格式或应用版本不符")
	}
	if manifest.ProductVersion != version || len(manifest.Components) > 10 {
		return invalidStatus(version, "组件清单格式或应用版本不符")
	}
	return legacyStatus(resourcesRoot, version, manifest)
}

func validV2Component(component Component, manifest Manifest, ids, directories map[string]bool) bool {
	if !idPattern.MatchString(component.ID) || ids[component.ID] || !relativePath(component.Directory) ||
		directories[strings.ToLower(component.Directory)] {
		return false
	}
	ids[component.ID] = true
	directories[strings.ToLower(component.Directory)] = true
	return component.Target.Platform == manifest.Target.Platform && component.Target.Arch == manifest.Target.Arch
}

func v2ComponentState(resourcesRoot string, component Component, compatible bool) (string, string) {
	if !compatible {
		return "unsupported_target", "组件目标与当前系统或架构不符"
	}
	validation, reason := "verified", "文件完整性与权限已校验；v2 安装尚未开放"
	total := int64(0)
	paths := map[string]bool{}
	base, _ := contained(resourcesRoot, component.Directory)
	for _, file := range component.Files {
		total += file.Size
		if !relativePath(file.Path) || paths[strings.ToLower(file.Path)] {
			validation, reason = "invalid", errRuntimeIntegrity
			break
		}
		paths[strings.ToLower(file.Path)] = true
		ok, why := checkRegular(base, file.Path, file.Size, file.SHA256, file.Mode)
		if !ok {
			validation, reason = "invalid", why
			if strings.Contains(why, "缺失") {
				validation = "missing"
			}
			break
		}
	}
	if total != component.Bytes {
		validation, reason = "invalid", errRuntimeIntegrity
	}
	return validation, reason
}

func statusV2(resourcesRoot, version string, manifest Manifest) Status {
	if manifest.ProductVersion != version || len(manifest.Components) == 0 || len(manifest.Components) > 10 {
		return invalidStatus(version, errRuntimeManifest)
	}
	ids := map[string]bool{}
	directories := map[string]bool{}
	compatible := manifest.Target.Platform == targetPlatform() && manifest.Target.Arch == targetArch()
	status := Status{Version: version, Components: []StatusComponent{}}
	for _, component := range manifest.Components {
		if !validV2Component(component, manifest, ids, directories) {
			return invalidStatus(version, errRuntimeManifest)
		}
		validation, reason := v2ComponentState(resourcesRoot, component, compatible)
		status.Components = append(status.Components, StatusComponent{
			ID: component.ID, Label: component.Label, Bytes: component.Bytes,
			Installed: validation == "verified", Validation: validation, Reason: reason,
		})
	}
	return status
}

func Install(resourcesRoot, version, id, source string) (any, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return nil, fmt.Errorf("运行组件目前仅支持 Windows，无法在当前平台安装")
	}
	manifest, err := readManifest(filepath.Join(resourcesRoot, "runtime-manifest.json"))
	if err != nil {
		return nil, err
	}
	if manifest.Schema != 1 || manifest.ProductVersion != version {
		return nil, fmt.Errorf("组件清单与应用版本不一致")
	}
	var component *Component
	for index := range manifest.Components {
		if manifest.Components[index].ID == id {
			component = &manifest.Components[index]
			break
		}
	}
	if component == nil {
		return nil, fmt.Errorf("未知组件")
	}
	if len(component.Files) == 0 {
		return nil, fmt.Errorf("组件文件清单为空，未安装")
	}
	target, err := contained(resourcesRoot, component.Directory)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("组件目录已存在；请使用完整更新包修复，避免覆盖正在使用的文件")
	}
	descriptorPath := filepath.Join(source, "component.json")
	info, err := os.Lstat(descriptorPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("请选择有效的同版本组件文件夹")
	}
	var descriptor struct {
		ID             string `json:"id"`
		ProductVersion string `json:"productVersion"`
	}
	raw, _ := os.ReadFile(descriptorPath)
	if json.Unmarshal(raw, &descriptor) != nil || descriptor.ID != id || descriptor.ProductVersion != version {
		return nil, fmt.Errorf("请选择与当前应用版本一致的组件包")
	}
	stage := filepath.Join(resourcesRoot, ".component-"+newID())
	if err := os.Mkdir(stage, 0o755); err != nil {
		return nil, fmt.Errorf("组件安装暂存失败")
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	payload := filepath.Join(source, "payload")
	for _, file := range component.Files {
		input, err := contained(payload, file.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(input)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != file.Size {
			return nil, fmt.Errorf("组件校验失败：%s", file.Path)
		}
		actual, err := hashFile(input)
		if err != nil || actual != file.SHA256 {
			return nil, fmt.Errorf("组件校验失败：%s", file.Path)
		}
		output, err := contained(stage, file.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return nil, fmt.Errorf("组件安装失败")
		}
		if err := copyFile(input, output); err != nil {
			return nil, fmt.Errorf("组件安装失败")
		}
		actual, err = hashFile(output)
		if err != nil || actual != file.SHA256 {
			return nil, fmt.Errorf("组件复制校验失败")
		}
	}
	if err := os.Rename(stage, target); err != nil {
		return nil, fmt.Errorf("组件安装失败")
	}
	published = true
	return map[string]any{"installed": true, "id": id}, nil
}

func copyFile(input, output string) error {
	source, err := os.Open(input)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return target.Sync()
}

func newID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(buffer)
}
