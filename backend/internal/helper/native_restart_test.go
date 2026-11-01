package helper

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const fakeNativeEnv = "ZYNOTE_FAKE_NATIVE_DIR"

// TestMain 让测试二进制在设置了环境变量时充当原生助手：第一次启动立即退出（模拟崩溃），之后正常应答。
func TestMain(m *testing.M) {
	if dir := os.Getenv(fakeNativeEnv); dir != "" {
		runFakeNative(dir)
		return
	}
	os.Exit(m.Run())
}

func runFakeNative(dir string) {
	counter := filepath.Join(dir, "starts")
	raw, _ := os.ReadFile(counter)
	starts, _ := strconv.Atoi(string(raw))
	starts++
	_ = os.WriteFile(counter, []byte(strconv.Itoa(starts)), 0o600)
	if starts == 1 {
		os.Exit(3)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var frame struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			continue
		}
		encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "result": map[string]any{"ok": true}})
		_, _ = os.Stdout.Write(append(encoded, '\n'))
	}
	os.Exit(0)
}

// 原生助手进程退出后，下一次调用应重新拉起进程，而不是一直报错到应用重启。
func TestNativeRestartsAfterExit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(fakeNativeEnv, dir)
	native := NewNative("pdf")
	native.Configure(os.Args[0], "", "", "", "")
	defer native.Dispose()
	if _, err := native.Call("pdf.text", map[string]any{}, 5000); err == nil {
		t.Fatal("first call must fail because the helper exits")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		native.mu.Lock()
		cleared := native.child == nil
		native.mu.Unlock()
		if cleared {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("exited helper was never cleared, so it can never restart")
		}
		time.Sleep(20 * time.Millisecond)
	}
	result, err := native.Call("pdf.text", map[string]any{}, 5000)
	if err != nil {
		t.Fatalf("helper must restart after exit, got %v", err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("unexpected result %s", result)
	}
}
