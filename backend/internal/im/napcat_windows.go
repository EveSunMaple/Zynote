//go:build windows && amd64

package im

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var napcatTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func napcatSupported() bool { return true }

type windowsJob struct {
	handle windows.Handle
}

func newWindowsJob() (*windowsJob, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("QQ 组件进程作业创建失败")
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(handle, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("QQ 组件进程作业配置失败")
	}
	return &windowsJob{handle: handle}, nil
}

func (job *windowsJob) assign(process *os.Process) error {
	if job == nil || job.handle == 0 || process == nil {
		return fmt.Errorf("QQ 组件进程作业无效")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return fmt.Errorf("QQ 组件进程句柄不可用")
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(job.handle, handle); err != nil {
		return fmt.Errorf("QQ 组件进程无法加入作业")
	}
	return nil
}

func (job *windowsJob) terminate() {
	if job == nil || job.handle == 0 {
		return
	}
	_ = windows.TerminateJobObject(job.handle, 1)
	_ = windows.CloseHandle(job.handle)
	job.handle = 0
}

func (runtime *NapcatRuntime) stopProcess() {
	runtime.mu.Lock()
	job := runtime.job
	child := runtime.child
	done := runtime.done
	runtime.job = nil
	runtime.child = nil
	runtime.done = nil
	runtime.running = false
	runtime.generation++
	runtime.mu.Unlock()
	if job != nil {
		job.terminate()
	}
	if child != nil && child.Process != nil {
		_ = child.Process.Kill()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (runtime *NapcatRuntime) start(ctx context.Context) (*NapcatSecrets, error) {
	runtime.setState("preparing", "")
	directory, value, err := runtime.prepare(ctx)
	if err != nil {
		runtime.setState("failed", "native-prepare-failed")
		return nil, err
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return nil, fmt.Errorf("QQ 组件所属服务已关闭")
	}
	runtime.value = value
	runtime.mu.Unlock()
	for _, port := range []int{value.WebUIPort, value.OneBotPort} {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			runtime.setState("failed", "native-port-busy")
			return nil, fmt.Errorf("QQ 组件端口被占用，未连接未知进程")
		}
		_ = listener.Close()
	}
	environment := sanitizedEnvironment()
	environment = append(environment,
		"NAPCAT_WORKDIR="+filepath.Join(directory, "napcat"),
		"NAPCAT_DISABLE_MULTI_PROCESS=1",
		"NAPCAT_WEBUI_SECRET_KEY="+value.WebUIToken,
	)
	runtime.mu.Lock()
	closed := runtime.closed
	runtime.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("QQ 组件所属服务已关闭")
	}
	job, err := newWindowsJob()
	if err != nil {
		runtime.setState("failed", "native-job-failed")
		return nil, err
	}
	command := exec.Command(filepath.Join(directory, "node.exe"), filepath.Join(directory, "index.js"))
	command.Dir = directory
	command.Env = environment
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := command.Start(); err != nil {
		job.terminate()
		runtime.setState("failed", "native-start-failed")
		return nil, fmt.Errorf("QQ 组件启动失败")
	}
	if err := job.assign(command.Process); err != nil {
		job.terminate()
		_ = command.Process.Kill()
		runtime.setState("failed", "native-job-failed")
		return nil, err
	}
	done := make(chan struct{})
	runtime.mu.Lock()
	runtime.job = job
	runtime.child = command
	runtime.done = done
	runtime.running = true
	runtime.generation++
	generation := runtime.generation
	runtime.state = "starting"
	runtime.code = ""
	runtime.mu.Unlock()
	go func() {
		_ = command.Wait()
		close(done)
		runtime.markExited(generation)
	}()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if !runtime.isRunning() {
			runtime.stopProcess()
			runtime.setState("failed", "native-exited")
			return nil, fmt.Errorf("QQ 组件启动后退出")
		}
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		hash := sha256.Sum256([]byte(value.WebUIToken + ".napcat"))
		response, err := LocalJSON(requestCtx, fmt.Sprintf("http://127.0.0.1:%d", value.WebUIPort),
			"/api/auth/login", map[string]any{"hash": hex.EncodeToString(hash[:])}, "")
		cancel()
		if err == nil {
			data, _ := response["data"].(map[string]any)
			if int(number(response["code"])) == 0 && text(data["Credential"]) != "" {
				runtime.setState("ready", "")
				return value, nil
			}
		}
		select {
		case <-ctx.Done():
			runtime.stopProcess()
			runtime.setState("failed", "native-start-cancelled")
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	runtime.stopProcess()
	runtime.setState("failed", "native-start-timeout")
	return nil, fmt.Errorf("QQ 组件启动超时，请检查组件状态")
}

func sanitizedEnvironment() []string {
	blocked := map[string]bool{
		"NAPCAT_QUICK_ACCOUNT": true, "NAPCAT_QUICK_PASSWORD": true, "NAPCAT_QUICK_PASSWORD_MD5": true,
		"NODE_OPTIONS": true, "NODE_PATH": true, "ELECTRON_RUN_AS_NODE": true,
		"NAPCAT_WORKER_PROCESS": true, "NAPCAT_WEBUI_PREFERRED_PORT": true,
	}
	values := []string{}
	for _, item := range os.Environ() {
		key := item
		if index := strings.IndexByte(item, '='); index >= 0 {
			key = item[:index]
		}
		if blocked[strings.ToUpper(key)] {
			continue
		}
		values = append(values, item)
	}
	return values
}

func secureRoot(root string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("QQ 组件根目录无效")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("QQ 组件根目录无效")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(root)) {
		return "", fmt.Errorf("QQ 组件根目录无效")
	}
	return filepath.Clean(root), nil
}

func ownedPath(root string, parts ...string) (string, error) {
	base, err := secureRoot(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(append([]string{base}, parts...)...)
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("QQ 组件路径越界")
	}
	current := base
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("QQ 组件路径无效")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("QQ 组件路径包含链接")
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(current)) {
			return "", fmt.Errorf("QQ 组件路径包含链接")
		}
	}
	return target, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hashZipEntry(entry *zip.File) (string, error) {
	reader, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func validateZipEntry(entry *zip.File) error {
	name := entry.Name
	if name == "" || strings.ContainsAny(name, ":\x00\\") || strings.HasPrefix(name, "/") {
		return fmt.Errorf("QQ 组件包路径无效")
	}
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("QQ 组件包路径无效")
		}
		if strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return fmt.Errorf("QQ 组件包路径无效")
		}
	}
	if entry.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("QQ 组件包不能包含链接")
	}
	return nil
}

func extractComponent(archive *zip.ReadCloser, stage string) error {
	total := uint64(0)
	writtenTotal := uint64(0)
	for _, entry := range archive.File {
		if err := validateZipEntry(entry); err != nil {
			return err
		}
		total += entry.UncompressedSize64
		if total > 500*1024*1024 {
			return fmt.Errorf("QQ 组件解压大小无效")
		}
		target := filepath.Join(stage, filepath.FromSlash(entry.Name))
		relative, err := filepath.Rel(stage, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("QQ 组件解压路径无效")
		}
		if entry.FileInfo().IsDir() || strings.HasSuffix(entry.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("QQ 组件解压失败")
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("QQ 组件解压失败")
		}
		source, err := entry.Open()
		if err != nil {
			return fmt.Errorf("QQ 组件解压失败")
		}
		destination, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			source.Close()
			return fmt.Errorf("QQ 组件解压失败")
		}
		written, copyErr := io.Copy(destination, io.LimitReader(source, 500*1024*1024+1))
		source.Close()
		closeErr := destination.Close()
		writtenTotal += uint64(written)
		if copyErr != nil || closeErr != nil || uint64(written) != entry.UncompressedSize64 ||
			writtenTotal > 500*1024*1024 {
			return fmt.Errorf("QQ 组件解压失败")
		}
	}
	return nil
}

func (runtime *NapcatRuntime) prepare(ctx context.Context) (string, *NapcatSecrets, error) {
	if !napcatSupported() {
		return "", nil, fmt.Errorf("内置 QQ 后台暂仅支持 Windows x64")
	}
	root, err := secureRoot(runtime.root)
	if err != nil {
		return "", nil, err
	}
	archivePath := runtime.archivePath()
	digest, err := hashFile(archivePath)
	if err != nil || digest != napcatSHA256 {
		return "", nil, fmt.Errorf("QQ 组件压缩包校验失败")
	}
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", nil, fmt.Errorf("QQ 组件压缩包不可用")
	}
	defer archive.Close()
	if len(archive.File) > 20000 {
		return "", nil, fmt.Errorf("QQ 组件文件数量无效")
	}
	target, err := ownedPath(root, "native-v"+napcatVersion)
	if err != nil {
		return "", nil, err
	}
	marker := filepath.Join(target, ".native-package.json")
	installed := false
	if raw, err := os.ReadFile(marker); err == nil {
		var descriptor struct {
			Version string `json:"version"`
			SHA256  string `json:"sha256"`
		}
		if json.Unmarshal(raw, &descriptor) != nil || descriptor.SHA256 != napcatSHA256 {
			return "", nil, fmt.Errorf("QQ 组件版本标记无效")
		}
		installed = true
	} else if !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("QQ 组件版本标记无效")
	}
	if !installed {
		if _, err := os.Lstat(target); err == nil {
			return "", nil, fmt.Errorf("QQ 组件安装未完成，请保留现场并修复")
		} else if !os.IsNotExist(err) {
			return "", nil, err
		}
		suffix, err := randomHex(8)
		if err != nil {
			return "", nil, err
		}
		stage, err := ownedPath(root, ".native-stage-"+suffix)
		if err != nil {
			return "", nil, err
		}
		if err := os.Mkdir(stage, 0o755); err != nil {
			return "", nil, fmt.Errorf("QQ 组件暂存目录不可用")
		}
		published := false
		defer func() {
			if !published {
				_ = os.RemoveAll(stage)
			}
		}()
		if err := extractComponent(archive, stage); err != nil {
			return "", nil, err
		}
		descriptor, _ := json.Marshal(map[string]string{"version": napcatVersion, "sha256": napcatSHA256})
		if err := os.WriteFile(filepath.Join(stage, ".native-package.json"), descriptor, 0o644); err != nil {
			return "", nil, fmt.Errorf("QQ 组件版本标记写入失败")
		}
		if err := os.Rename(stage, target); err != nil {
			return "", nil, fmt.Errorf("QQ 组件发布失败")
		}
		published = true
	}
	entries := map[string]*zip.File{}
	for _, entry := range archive.File {
		entries[entry.Name] = entry
	}
	for _, name := range []string{"node.exe", "index.js", "napcat/napcat.mjs", "wrapper.node", "package.json", "config.json"} {
		entry := entries[name]
		if entry == nil {
			return "", nil, fmt.Errorf("QQ 组件缺少必要文件")
		}
		path, err := ownedPath(root, "native-v"+napcatVersion, filepath.FromSlash(name))
		if err != nil {
			return "", nil, err
		}
		diskHash, diskErr := hashFile(path)
		zipHash, zipErr := hashZipEntry(entry)
		if diskErr != nil || zipErr != nil || diskHash != zipHash {
			return "", nil, fmt.Errorf("QQ 组件执行文件已改变")
		}
	}
	value, err := runtime.loadOrCreateOwner(root)
	if err != nil {
		return "", nil, err
	}
	config, err := ownedPath(root, "native-v"+napcatVersion, "napcat", "config")
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(config, 0o755); err != nil {
		return "", nil, fmt.Errorf("QQ 组件配置目录不可用")
	}
	if raw, err := os.ReadFile(filepath.Join(config, ".env")); err == nil {
		for _, line := range strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				return "", nil, fmt.Errorf("QQ 组件环境文件已改变，未启动")
			}
		}
	} else if !os.IsNotExist(err) {
		return "", nil, err
	}
	webui, _ := json.Marshal(map[string]any{
		"host": "127.0.0.1", "port": value.WebUIPort, "token": value.WebUIToken, "autoLoginAccount": "",
	})
	if err := os.WriteFile(filepath.Join(config, "webui.json"), webui, 0o600); err != nil {
		return "", nil, fmt.Errorf("QQ 组件 WebUI 配置写入失败")
	}
	settingsPath := filepath.Join(config, "napcat.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(settingsPath); err != nil || json.Unmarshal(raw, &settings) != nil {
		return "", nil, fmt.Errorf("QQ 组件配置无效")
	}
	settings["fileLog"] = false
	settings["consoleLog"] = false
	encoded, _ := json.Marshal(settings)
	if err := os.WriteFile(settingsPath, encoded, 0o600); err != nil {
		return "", nil, fmt.Errorf("QQ 组件配置写入失败")
	}
	return target, value, nil
}

func randomHex(length int) (string, error) {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("QQ 组件随机数不可用")
	}
	return hex.EncodeToString(buffer), nil
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("QQ 组件端口分配失败")
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func validSecrets(value *NapcatSecrets) bool {
	return value != nil && value.WebUIPort >= 1024 && value.WebUIPort <= 65535 &&
		value.OneBotPort >= 1024 && value.OneBotPort <= 65535 && value.WebUIPort != value.OneBotPort &&
		napcatTokenPattern.MatchString(value.WebUIToken) && napcatTokenPattern.MatchString(value.OneBotToken)
}

func (runtime *NapcatRuntime) loadOrCreateOwner(root string) (*NapcatSecrets, error) {
	path, err := ownedPath(root, "native-owner.dpapi")
	if err != nil {
		return nil, err
	}
	if raw, err := os.ReadFile(path); err == nil {
		if runtime.crypt == nil {
			return nil, fmt.Errorf("系统加密桥不可用")
		}
		plain, err := runtime.crypt(raw, true)
		if err != nil {
			return nil, err
		}
		value := &NapcatSecrets{}
		if json.Unmarshal(plain, value) != nil || !validSecrets(value) {
			return nil, fmt.Errorf("QQ 组件凭据无效")
		}
		return value, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if runtime.crypt == nil {
		return nil, fmt.Errorf("系统加密桥不可用")
	}
	webuiPort, err := freePort()
	if err != nil {
		return nil, err
	}
	onebotPort, err := freePort()
	if err != nil {
		return nil, err
	}
	for onebotPort == webuiPort {
		onebotPort, err = freePort()
		if err != nil {
			return nil, err
		}
	}
	webuiToken, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	onebotToken, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	value := &NapcatSecrets{WebUIPort: webuiPort, OneBotPort: onebotPort, WebUIToken: webuiToken, OneBotToken: onebotToken}
	raw, _ := json.Marshal(value)
	encrypted, err := runtime.crypt(raw, false)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return runtime.loadOrCreateOwner(root)
		}
		return nil, fmt.Errorf("QQ 组件凭据写入失败")
	}
	if _, err := file.Write(encrypted); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("QQ 组件凭据写入失败")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("QQ 组件凭据写入失败")
	}
	return value, nil
}
