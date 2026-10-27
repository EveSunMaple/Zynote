package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSettingsModelsProbeAndTest(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	auth := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		auth = append(auth, request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/v1/models":
			fmt.Fprint(writer, `{"data":[{"id":"m1"},{"id":"m2"}]}`)
		case "/v1/chat/completions":
			fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"连接成功"}}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","browserWidth":420,"browserOpen":true,"maxMinutes":60,"maxGB":1.5,"compress":false,"prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"m1","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": dataRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	raw, errReply := f.call("settings:models", map[string]any{
		"profile": map[string]any{
			"id": "qwen", "name": "测试", "baseUrl": server.URL + "/v1", "model": "m1",
			"visionModel": "", "provider": "qwen", "apiFormat": "chat_completions", "apiKey": "plain-key",
		},
		"persist": true,
	})
	if errReply != nil {
		t.Fatalf("models failed: %+v", errReply)
	}
	var models []string
	if err := json.Unmarshal(raw, &models); err != nil || len(models) != 2 || models[0] != "m1" {
		t.Fatalf("models mismatch: %s %v", raw, err)
	}
	raw, errReply = f.call("settings:probe", map[string]any{
		"profile": map[string]any{
			"id": "qwen", "name": "测试", "baseUrl": server.URL + "/v1", "model": "m1",
			"provider": "qwen", "apiFormat": "chat_completions",
		},
	})
	if errReply != nil {
		t.Fatalf("probe failed: %+v", errReply)
	}
	var reply string
	if err := json.Unmarshal(raw, &reply); err != nil || reply != "连接成功" {
		t.Fatalf("probe reply mismatch: %s %v", raw, err)
	}
	raw, errReply = f.call("settings:test", nil)
	if errReply != nil || json.Unmarshal(raw, &reply) != nil || reply != "连接成功" {
		t.Fatalf("test failed: %s %+v", raw, errReply)
	}
	if len(auth) != 3 || auth[0] != "Bearer plain-key" || auth[2] != "Bearer test-key" {
		t.Fatalf("authorization mismatch: %+v", auth)
	}
	settingsRaw, errReply := f.call("settings:get", nil)
	if errReply != nil {
		t.Fatalf("settings:get failed: %+v", errReply)
	}
	var settings struct {
		Profiles []struct {
			Models []string `json:"models"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(settingsRaw, &settings); err != nil || len(settings.Profiles) != 1 ||
		len(settings.Profiles[0].Models) != 2 || settings.Profiles[0].Models[1] != "m2" {
		t.Fatalf("cached models mismatch: %s %v", settingsRaw, err)
	}
}
