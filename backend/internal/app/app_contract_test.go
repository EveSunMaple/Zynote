package app_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/app"
	"github.com/EveSunMaple/Zynote/backend/internal/helper"
	"github.com/EveSunMaple/Zynote/backend/internal/transport"
	_ "modernc.org/sqlite"
)

type fixture struct {
	t          *testing.T
	inWrite    *io.PipeWriter
	outRead    *bufio.Reader
	lastResult json.RawMessage
	events     []wireEnvelope
}

type testReverse struct{ server *transport.Server }

func (reverse testReverse) Call(method string, params any, timeoutMS int) (json.RawMessage, error) {
	if method == "crypto.unprotect" {
		return json.RawMessage(`{"text":"test-key"}`), nil
	}
	if method == "crypto.protect" {
		return json.RawMessage(`{"value":"cHJvdGVjdGVk"}`), nil
	}
	if method == "tools.configure" || method == "shell.openPath" || method == "shell.showItemInFolder" {
		return json.RawMessage(`true`), nil
	}
	if method == "shell.printHtmlPdf" {
		values, _ := params.(map[string]any)
		path, _ := values["path"].(string)
		_ = os.WriteFile(path, []byte("%PDF-1.4\n%%EOF\n"), 0o644)
		return json.RawMessage(`true`), nil
	}
	if method == "shell.trashItem" {
		return json.RawMessage(`true`), nil
	}
	if method == "crypto.protect-bytes" || method == "crypto.unprotect-bytes" {
		values, _ := params.(map[string]any)
		encoded, _ := json.Marshal(map[string]any{"value": values["value"]})
		return encoded, nil
	}
	return reverse.server.Call(method, params, timeoutMS)
}

func startCore(t *testing.T, dataRoot, resourcesRoot string) *fixture {
	t.Helper()
	core := app.New()
	server := transport.NewServer(core)
	core.Wire(testReverse{server: server})
	core.SetNotifier(server)
	embed := helper.NewNative("embed")
	core.SetEmbedNative(embed)
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	go func() {
		_ = server.Serve(context.Background(), serverInR, serverOutW)
	}()
	t.Cleanup(func() {
		embed.Dispose()
		_ = serverInW.Close()
		_ = serverOutW.Close()
	})
	return &fixture{t: t, inWrite: serverInW, outRead: bufio.NewReader(serverOutR)}
}

type wireEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *fixture) call(method string, params map[string]any) (json.RawMessage, *wireError) {
	f.t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "id": "t-" + method, "method": method}
	if params != nil {
		request["params"] = params
	}
	line, err := json.Marshal(request)
	if err != nil {
		f.t.Fatal(err)
	}
	f.inWrite.Write(append(line, '\n'))
	replies := f.drain(1)
	if len(replies) == 0 {
		f.t.Fatalf("no reply for %s", method)
	}
	f.lastResult = replies[0].Result
	return replies[0].Result, replies[0].Error
}

func (f *fixture) drain(count int) []wireEnvelope {
	f.t.Helper()
	var replies []wireEnvelope
	for len(replies) < count {
		line, err := f.outRead.ReadString('\n')
		if err != nil {
			f.t.Fatalf("read reply: %v", err)
		}
		var env wireEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			f.t.Fatalf("bad reply %q: %v", line, err)
		}
		if env.Method != "" {
			f.events = append(f.events, env)
			continue
		}
		if env.ID == nil {
			continue
		}
		replies = append(replies, env)
	}
	return replies
}

func writeFixtureFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestP0Contract(t *testing.T) {
	dataRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{
		"theme": "dark", "browserWidth": 480, "browserOpen": false,
		"maxMinutes": 30, "maxGB": 2.5, "compress": true,
		"prompts": {"chat": "custom"},
		"searchProvider": "searxng", "searchEndpoint": "https://search.example",
		"codex": {"legacy": true}, "videoPolicyVersion": 2,
		"qwenProfile": {"id": "qwen", "name": "测试", "baseUrl": "https://api.example/v1", "model": "qwen-max", "visionModel": "qwen-vl", "provider": "qwen", "apiFormat": "chat_completions", "models": ["qwen-max"]}
	}`)
	writeFixtureFile(t, dataRoot, "identity.json", `{"userName":"连酱","assistantName":"梅酱","userAvatar":"","assistantAvatar":"","persona":"自定义人格"}`)
	writeFixtureFile(t, dataRoot, "appearance.json", `{"global":{"background":"mint","image":"","opacity":0.5,"blur":4,"scrim":0.3,"accent":"blue","fontSize":18,"lineHeight":1.8,"avatarSize":44,"avatarShape":"rounded","showNames":false,"assistantBubble":true,"bubbleRadius":20,"messageGap":30,"contentWidth":900},"courses":{}}`)
	writeFixtureFile(t, resourcesRoot, "prompts/notes.txt", "默认笔记提示词")

	f := startCore(t, dataRoot, resourcesRoot)

	state, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": dataRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false,
	})
	if errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	var hello struct {
		Protocol     int    `json:"protocol"`
		Platform     string `json:"platform"`
		Capabilities []string
	}
	if err := json.Unmarshal(state, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Protocol != 1 || hello.Platform != "darwin" || len(hello.Capabilities) != 135 {
		t.Fatalf("unexpected hello: %+v", hello)
	}

	if _, errReply = f.call("app:info", nil); errReply != nil {
		t.Fatalf("app:info failed: %+v", errReply)
	}
	var info map[string]any
	if err := json.Unmarshal(f.lastResult, &info); err != nil {
		t.Fatal(err)
	}
	if info["version"] != "0.0.1" || info["platform"] != "darwin" {
		t.Fatalf("unexpected app:info: %v", info)
	}

	if _, errReply = f.call("settings:get", nil); errReply != nil {
		t.Fatalf("settings:get failed: %+v", errReply)
	}
	var settings map[string]any
	if err := json.Unmarshal(f.lastResult, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["theme"] != "dark" || settings["connection"] != "qwen" || settings["activeProfile"] != "qwen" {
		t.Fatalf("unexpected settings passthrough: %v", settings)
	}
	if _, ok := settings["codex"]; ok {
		t.Fatalf("legacy codex leaked into settings:get")
	}
	profiles, ok := settings["profiles"].([]any)
	if !ok || len(profiles) != 1 {
		t.Fatalf("unexpected profiles: %v", settings["profiles"])
	}
	profile := profiles[0].(map[string]any)
	if profile["hasKey"] != false || profile["name"] != "测试" || profile["keyLength"] != float64(0) {
		t.Fatalf("unexpected profile: %v", profile)
	}
	prompts := settings["prompts"].(map[string]any)
	if prompts["chat"] != "custom" || prompts["notes"] != "默认笔记提示词" {
		t.Fatalf("prompts passthrough broken: %v", prompts)
	}

	if _, errReply = f.call("identity:get", nil); errReply != nil {
		t.Fatalf("identity:get failed: %+v", errReply)
	}
	var identity map[string]any
	if err := json.Unmarshal(f.lastResult, &identity); err != nil {
		t.Fatal(err)
	}
	if identity["assistantName"] != "Zynote" || identity["persona"] != "自定义人格" {
		t.Fatalf("identity mapping broken: %v", identity)
	}

	if _, errReply = f.call("appearance:get", nil); errReply != nil {
		t.Fatalf("appearance:get failed: %+v", errReply)
	}
	var appearance map[string]any
	if err := json.Unmarshal(f.lastResult, &appearance); err != nil {
		t.Fatal(err)
	}
	global := appearance["global"].(map[string]any)
	if global["background"] != "mint" || global["fontSize"] != float64(18) {
		t.Fatalf("appearance passthrough broken: %v", global)
	}

	if _, errReply = f.call("db:health", nil); errReply != nil {
		t.Fatalf("db:health on missing db should be a report, got %+v", errReply)
	}
	var health map[string]any
	if err := json.Unmarshal(f.lastResult, &health); err != nil {
		t.Fatal(err)
	}
	if health["exists"] != false {
		t.Fatalf("unexpected health on missing db: %v", health)
	}

	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE courses(id TEXT PRIMARY KEY, name TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO courses(id,name) VALUES('a','课程A')"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	if _, errReply = f.call("db:health", nil); errReply != nil {
		t.Fatalf("db:health failed: %+v", errReply)
	}
	if err := json.Unmarshal(f.lastResult, &health); err != nil {
		t.Fatal(err)
	}
	if health["exists"] != true || health["ok"] != true || health["integrity"] != "ok" {
		t.Fatalf("unexpected health: %v", health)
	}
	if health["tables"].(map[string]any)["courses"] != float64(1) {
		t.Fatalf("unexpected course count: %v", health["tables"])
	}

	if _, errReply = f.call("db:backup", nil); errReply != nil {
		t.Fatalf("db:backup failed: %+v", errReply)
	}
	var backup map[string]any
	if err := json.Unmarshal(f.lastResult, &backup); err != nil {
		t.Fatal(err)
	}
	backupPath, _ := backup["path"].(string)
	if !strings.HasPrefix(backupPath, dataRoot) || backup["bytes"].(float64) <= 0 {
		t.Fatalf("unexpected backup: %v", backup)
	}

	if _, errReply = f.call("nope:action", nil); errReply == nil || errReply.Code != "INPUT_INVALID" {
		t.Fatalf("unknown action should be INPUT_INVALID, got %+v", errReply)
	}
}
