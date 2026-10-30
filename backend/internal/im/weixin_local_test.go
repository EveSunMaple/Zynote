package im

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/helper"
)

func TestWeixinLocalAvailabilityRequiresConfiguredHelper(t *testing.T) {
	local := NewWeixinLocal(nil)
	if local.Available() {
		t.Fatal("missing helper must not be available")
	}
	binary := filepath.Join(t.TempDir(), "zynote-native.exe")
	if err := os.WriteFile(binary, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	native := helper.NewNative("wechat")
	native.Configure(binary, "", "", "", "")
	local.SetNative(native)
	if got, want := local.Available(), weixinSupported(); got != want {
		t.Fatalf("availability mismatch: got %v want %v", got, want)
	}
}

func TestWeixinSessionShapeIsCompatible(t *testing.T) {
	session := map[string]any{
		"reader_id": "0123456789abcdef0123456789abcdef",
		"cursors": map[string]any{
			"alice": map[string]any{"message_0.db": []any{"12", "34"}},
		},
	}
	if got := sessionReaderID(session); got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("reader id mismatch: %q", got)
	}
	encoded, _ := json.Marshal(session)
	if string(encoded) == "" {
		t.Fatal("session must stay serializable")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(root, "", testCrypt)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	initial, _ := service.Store.Listing()
	created, err := service.Store.Change(map[string]any{
		"action": "create", "expected_revision": initial["revision"], "name": "微信", "platform": "weixin_local",
	})
	if err != nil {
		t.Fatal(err)
	}
	row := created.(map[string]any)["connections"].([]Connection)[0]
	bound, err := service.Store.Bind(row.ID, row.Revision, "wxid_legacy", session, map[string]any{"receive": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	private, err := service.Store.PrivateState(bound.ID, bound.Revision)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := private.Session.(map[string]any)
	if sessionReaderID(saved) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("saved session mismatch: %+v", saved)
	}
	cursors, _ := saved["cursors"].(map[string]any)
	if cursors["alice"] == nil {
		t.Fatalf("saved cursors mismatch: %+v", saved)
	}
}

func TestWeixinSendIsExplicitlyReadOnly(t *testing.T) {
	if !weixinSupported() {
		t.Skip("local WeChat reader is Windows x64 only")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(root, "", testCrypt)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	binary := filepath.Join(t.TempDir(), "zynote-native.exe")
	if err := os.WriteFile(binary, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	native := helper.NewNative("wechat")
	native.Configure(binary, "", "", "", "")
	service.SetWeixinNative(native)
	initial, _ := service.Store.Listing()
	created, err := service.Store.Change(map[string]any{
		"action": "create", "expected_revision": initial["revision"], "name": "微信", "platform": "weixin_local",
	})
	if err != nil {
		t.Fatal(err)
	}
	row := created.(map[string]any)["connections"].([]Connection)[0]
	bound, err := service.Store.Bind(row.ID, row.Revision, "wxid_test", map[string]any{"reader_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{
		ConnectionID: bound.ID, Revision: bound.Revision, Platform: bound.Platform,
		Account: bound.Account, LoginEpoch: bound.LoginEpoch, Peer: "alice",
	}
	_, err = service.scopeAction(context.Background(), "send", scopeMap(scope))
	if err == nil || err.Error() != "本机个人微信仅支持只读和草稿" {
		t.Fatalf("send must be rejected, got %v", err)
	}
}

func TestNapcatAvailabilityDoesNotClaimMissingComponent(t *testing.T) {
	runtime := NewNapcatRuntime(t.TempDir(), t.TempDir(), testCrypt)
	available, reason := runtime.Availability()
	if available {
		t.Fatal("missing component must not be available")
	}
	if reason == "" {
		t.Fatal("missing component needs a reason")
	}
	status := runtime.Status()
	if status["state"] != "stopped" || status["version"] != napcatVersion || status["owned_running"] != false {
		t.Fatalf("status mismatch: %+v", status)
	}
}
