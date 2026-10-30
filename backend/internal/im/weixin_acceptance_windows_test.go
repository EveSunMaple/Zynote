//go:build windows && amd64

package im

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/helper"
)

// TestWeixinRealLocal is opt-in because it reads the signed-in WeChat process.
// It never prints account ids, contacts or message bodies.
func TestWeixinRealLocal(t *testing.T) {
	if os.Getenv("ZYNOTE_WEIXIN_ACCEPTANCE") != "1" {
		t.Skip("set ZYNOTE_WEIXIN_ACCEPTANCE=1 to read the local WeChat process")
	}
	nativePath := os.Getenv("ZYNOTE_NATIVE_PATH")
	if nativePath == "" {
		path, err := filepath.Abs(filepath.Join("..", "..", "..", "native", "target", "release", "zynote-native.exe"))
		if err != nil {
			t.Fatal(err)
		}
		nativePath = path
	}
	if info, err := os.Stat(nativePath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("native helper missing at %s", nativePath)
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
	native := helper.NewNative("wechat")
	native.Configure(nativePath, "", "", "", "")
	service.SetWeixinNative(native)
	defer native.Dispose()

	initial, _ := service.Store.Listing()
	created, err := service.Execute(context.Background(), "change", map[string]any{
		"action": "create", "expected_revision": initial["revision"], "name": "本机微信", "platform": "weixin_local",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	row := created.(map[string]any)["connections"].([]Connection)[0]

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	raw, err := service.Execute(ctx, "personal-processes", map[string]any{}, "")
	if err != nil {
		t.Fatal(err)
	}
	processes, _ := raw.(map[string]any)["processes"].([]any)
	if len(processes) == 0 {
		t.Skip("no signed-in WeChat process found")
	}
	var probe map[string]any
	var account string
	for _, item := range processes {
		process, _ := item.(map[string]any)
		result, err := service.Execute(ctx, "personal-probe", map[string]any{
			"connection_id": row.ID, "revision": row.Revision, "pid": number(process["pid"]), "created": number(process["created"]),
		}, "")
		if err != nil {
			continue
		}
		probe = result.(map[string]any)
		account = text(probe["account"])
		break
	}
	if probe == nil {
		t.Fatal("no WeChat process passed identity and database verification")
	}
	confirmed, err := service.Execute(ctx, "personal-confirm", map[string]any{
		"connection_id": row.ID, "revision": row.Revision, "probe_id": text(probe["probe_id"]),
		"account": account, "user_confirmed": true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	bound := service.Store
	_ = confirmed
	connection, err := bound.Capture(row.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if connection.Account == "" || !connection.HasSession {
		t.Fatal("local WeChat binding was not persisted")
	}
	private, err := bound.PrivateState(connection.ID, connection.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if sessionReaderID(private.Session) == "" {
		t.Fatal("local WeChat session lost reader_id")
	}
	if _, err := service.weixin.Verify(ctx, private.Session, "not-the-bound-account"); err == nil {
		t.Fatal("wrong account must be rejected")
	}

	directory, err := service.Execute(ctx, "directory", map[string]any{
		"connection_id": connection.ID, "revision": connection.Revision, "platform": connection.Platform,
		"account": connection.Account, "login_epoch": connection.LoginEpoch, "peer": "_directory_",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	rawPeers, _ := directory.(map[string]any)["peers"].([]any)
	peers := make([]map[string]any, 0, len(rawPeers))
	for _, item := range rawPeers {
		peer, _ := item.(map[string]any)
		peers = append(peers, peer)
	}
	if len(peers) == 0 {
		t.Fatal("local WeChat contact table returned no peers")
	}
	peer := ""
	saved := 0
	messages := []Message{}
	for index, item := range peers {
		if index >= 50 || (len(peer) > 0 && saved > 0) {
			break
		}
		candidate := text(item["peer"])
		if candidate == "" {
			continue
		}
		synced, err := service.Execute(ctx, "sync", map[string]any{
			"connection_id": connection.ID, "revision": connection.Revision, "platform": connection.Platform,
			"account": connection.Account, "login_epoch": connection.LoginEpoch, "peer": candidate,
		}, "")
		if err != nil {
			continue
		}
		count := int(number(synced.(map[string]any)["saved"]))
		if count > 0 {
			peer = candidate
			saved = count
			break
		}
		if peer == "" {
			peer = candidate
		}
	}
	if peer == "" {
		t.Fatal("peer id missing")
	}
	read, err := service.Execute(ctx, "read", map[string]any{
		"connection_id": connection.ID, "revision": connection.Revision, "platform": connection.Platform,
		"account": connection.Account, "login_epoch": connection.LoginEpoch, "peer": peer, "limit": 5,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	messages, _ = read.(map[string]any)["messages"].([]Message)
	for _, message := range messages {
		if len([]rune(message.Text)) > 6000 || message.MID == "" {
			t.Fatal("read message shape is invalid")
		}
	}
	service.weixin.InvalidateConnection(ctx, connection.ID)
	if _, err := service.weixin.Verify(ctx, private.Session, account); err == nil {
		t.Fatal("invalidated reader must fail verification")
	}
	t.Logf("local WeChat acceptance completed: processes=%d peers=%d messages=%d", len(processes), len(peers), len(messages))
}
