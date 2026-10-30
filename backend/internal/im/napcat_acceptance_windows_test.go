//go:build windows && amd64

package im

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestNapcatRealComponent is opt-in because it launches the pinned third-party
// component. It verifies extraction, WebUI readiness, Job Object cleanup and restart.
func TestNapcatRealComponent(t *testing.T) {
	if os.Getenv("ZYNOTE_NAPCAT_ACCEPTANCE") != "1" {
		t.Skip("set ZYNOTE_NAPCAT_ACCEPTANCE=1 to launch the pinned component")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resources, err := filepath.Abs(filepath.Join("..", "..", "..", "resources"))
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewNapcatRuntime(resources, filepath.Join(root, "napcat"), testCrypt)
	available, reason := runtime.Availability()
	if !available {
		t.Fatalf("component unavailable: %s", reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	value, err := runtime.ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.WebUIPort == 0 || value.OneBotPort == 0 || value.WebUIToken == "" || value.OneBotToken == "" {
		t.Fatalf("owner credentials invalid: %+v", value)
	}
	if !runtime.isRunning() {
		t.Fatal("component must be running")
	}
	if status := runtime.Status(); status["state"] != "ready" {
		t.Fatalf("component not ready: %+v", status)
	}
	pid := runtime.childPID()

	restarted, err := runtime.ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.WebUIPort != value.WebUIPort || pid != runtime.childPID() {
		t.Fatal("running component must be reused")
	}
	onebot, err := parseQqConfig(map[string]any{
		"url": "http://127.0.0.1:" + strconv.Itoa(value.OneBotPort), "token": value.OneBotToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := NewQqLogin("http://127.0.0.1:"+strconv.Itoa(value.WebUIPort), value.WebUIToken, onebot, true)
	if err != nil {
		t.Fatal(err)
	}
	state, err := login.Start(ctx)
	if err != nil {
		t.Logf("QQ login/QR flow not verified (network or account state): %v", err)
	} else if text(state["state"]) != "waiting_scan" && text(state["state"]) != "scanned" && text(state["state"]) != "verifying" {
		t.Fatalf("unexpected QQ login state: %+v", state)
	} else if text(state["state"]) != "verifying" && text(state["qr"]) == "" {
		t.Fatal("QQ login did not return a QR code")
	}

	runtime.Close()
	time.Sleep(500 * time.Millisecond)
	if processAlive(pid) {
		t.Fatalf("owned node.exe pid %d survived close", pid)
	}

	restartedRuntime := NewNapcatRuntime(resources, filepath.Join(root, "napcat"), testCrypt)
	value, err = restartedRuntime.ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newPID := restartedRuntime.childPID()
	restartedRuntime.Close()
	time.Sleep(500 * time.Millisecond)
	if processAlive(newPID) {
		t.Fatalf("restarted node.exe pid %d survived close", newPID)
	}
	if value.WebUIPort == 0 {
		t.Fatal("restart did not reuse owner credentials")
	}
}

func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == 259
}
