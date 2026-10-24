package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeReverse struct{ calls []string }

func (reverse *fakeReverse) Call(method string, params any, timeoutMS int) (json.RawMessage, error) {
	reverse.calls = append(reverse.calls, method)
	switch method {
	case "crypto.protect":
		values := params.(map[string]string)
		return json.RawMessage(`{"value":"cipher:` + values["value"] + `"}`), nil
	case "crypto.unprotect":
		return json.RawMessage(`{"text":"test-key"}`), nil
	}
	return json.RawMessage(`true`), nil
}

func settingsSavePayload(profile map[string]any, extra map[string]any) json.RawMessage {
	value := map[string]any{
		"profiles": []any{profile}, "activeProfile": "qwen", "theme": "dark",
		"browserWidth": 1000, "browserOpen": true, "maxMinutes": 45, "maxGB": 2,
		"compress": true, "searchProvider": "searxng", "searchEndpoint": " https://search.example ",
		"prompts": map[string]string{"notes": "自定义"},
	}
	for key, item := range extra {
		value[key] = item
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func readSettingsFile(t *testing.T, root string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSaveSettingsEncryptsAndMerges(t *testing.T) {
	root := t.TempDir()
	promptsRoot := t.TempDir()
	writeTestSettings(t, root, `{"legacy":"keep","qwenProfile":{"id":"qwen","name":"旧配置","baseUrl":"https://api.example/v1","model":"m1","visionModel":"","apiFormat":"chat_completions","provider":"qwen","parameters":{},"encryptedKey":"old-cipher"}}`)
	reverse := &fakeReverse{}
	result, err := SaveSettings(root, promptsRoot, reverse, settingsSavePayload(map[string]any{
		"id": "qwen", "name": " 新配置 ", "baseUrl": "https://api.example/v1/", "model": "m2",
		"visionModel": "v1", "apiKey": " new-key ", "parameters": map[string]any{"m2": map[string]any{"maxTokens": 512}},
	}, map[string]any{"searchKey": "tavily-key"}), false)
	if err != nil {
		t.Fatal(err)
	}
	file := readSettingsFile(t, root)
	if file["legacy"] != "keep" || file["theme"] != "dark" || file["browserWidth"] != float64(900) ||
		file["maxMinutes"] != float64(45) || file["searchProvider"] != "searxng" ||
		file["searchEndpoint"] != "https://search.example" || file["videoPolicyVersion"] != float64(2) {
		t.Fatalf("merged settings mismatch: %+v", file)
	}
	profile := file["qwenProfile"].(map[string]any)
	if profile["name"] != "新配置" || profile["baseUrl"] != "https://api.example/v1" ||
		profile["encryptedKey"] != "cipher:new-key" {
		t.Fatalf("profile mismatch: %+v", profile)
	}
	models := profile["models"].([]any)
	if len(models) != 2 || models[0] != "m2" || models[1] != "v1" {
		t.Fatalf("models mismatch: %+v", models)
	}
	if file["encryptedSearchKey"] != "cipher:tavily-key" {
		t.Fatalf("search key mismatch: %+v", file["encryptedSearchKey"])
	}
	if result.Settings["hasSearchKey"] != true || !result.ConnectionChanged {
		t.Fatalf("result mismatch: %+v", result)
	}
}

func TestSaveSettingsRetainsCredentials(t *testing.T) {
	root := t.TempDir()
	promptsRoot := t.TempDir()
	writeTestSettings(t, root, `{"qwenProfile":{"id":"qwen","name":"旧配置","baseUrl":"https://api.example/v1","model":"m1","visionModel":"","apiFormat":"chat_completions","provider":"qwen","parameters":{},"encryptedKey":"old-cipher"},"encryptedSearchKey":"old-search"}`)
	reverse := &fakeReverse{}
	result, err := SaveSettings(root, promptsRoot, reverse, settingsSavePayload(map[string]any{
		"id": "qwen", "name": "旧配置", "baseUrl": "https://api.example/v1", "model": "m1",
	}, map[string]any{"searchKey": ""}), false)
	if err != nil {
		t.Fatal(err)
	}
	file := readSettingsFile(t, root)
	profile := file["qwenProfile"].(map[string]any)
	if profile["encryptedKey"] != "old-cipher" {
		t.Fatalf("retained key mismatch: %+v", profile)
	}
	if _, exists := file["encryptedSearchKey"]; exists {
		t.Fatalf("search key should be cleared: %+v", file)
	}
	if result.Settings["hasSearchKey"] != false {
		t.Fatalf("hasSearchKey mismatch: %+v", result.Settings)
	}
	if len(reverse.calls) < 2 || reverse.calls[1] != "crypto.unprotect" {
		t.Fatalf("credentials must be verified: %v", reverse.calls)
	}
}

func TestSaveSettingsRejectsAddressChangeWithoutKey(t *testing.T) {
	root := t.TempDir()
	writeTestSettings(t, root, `{"qwenProfile":{"id":"qwen","name":"旧配置","baseUrl":"https://api.example/v1","model":"m1","visionModel":"","apiFormat":"chat_completions","provider":"qwen","parameters":{},"encryptedKey":"old-cipher"}}`)
	_, err := SaveSettings(root, t.TempDir(), &fakeReverse{}, settingsSavePayload(map[string]any{
		"id": "qwen", "name": "旧配置", "baseUrl": "https://other.example/v1", "model": "m1",
	}, nil), false)
	if err == nil || !strings.Contains(err.Error(), "API 地址已改变") {
		t.Fatalf("address guard mismatch: %v", err)
	}
}

func TestSaveSettingsBusyGuard(t *testing.T) {
	root := t.TempDir()
	writeTestSettings(t, root, `{"qwenProfile":{"id":"qwen","name":"旧配置","baseUrl":"https://api.example/v1","model":"m1","visionModel":"","apiFormat":"chat_completions","provider":"qwen","parameters":{},"encryptedKey":"old-cipher"}}`)
	changed := settingsSavePayload(map[string]any{
		"id": "qwen", "name": "旧配置", "baseUrl": "https://api.example/v1", "model": "m2",
	}, nil)
	if _, err := SaveSettings(root, t.TempDir(), &fakeReverse{}, changed, true); err == nil ||
		!strings.Contains(err.Error(), "任务运行中") {
		t.Fatalf("busy guard mismatch: %v", err)
	}
	unchanged := settingsSavePayload(map[string]any{
		"id": "qwen", "name": "旧配置", "baseUrl": "https://api.example/v1", "model": "m1",
	}, map[string]any{"theme": "light"})
	if _, err := SaveSettings(root, t.TempDir(), &fakeReverse{}, unchanged, true); err != nil {
		t.Fatalf("theme-only save must pass: %v", err)
	}
}

func TestSaveSettingsValidation(t *testing.T) {
	root := t.TempDir()
	writeTestSettings(t, root, `{}`)
	_, err := SaveSettings(root, t.TempDir(), &fakeReverse{}, settingsSavePayload(map[string]any{
		"id": "qwen", "name": "配置", "baseUrl": "https://api.example/v1", "model": "m1",
	}, map[string]any{"maxMinutes": 0}), false)
	if err == nil || !strings.Contains(err.Error(), "视频限制") {
		t.Fatalf("limits mismatch: %v", err)
	}
	_, err = SaveSettings(root, t.TempDir(), &fakeReverse{}, settingsSavePayload(map[string]any{
		"id": "qwen", "name": "配置", "baseUrl": "https://api.example/v1", "model": "m1",
		"parameters": map[string]any{"m1": map[string]any{"temperature": 3}},
	}, nil), false)
	if err == nil || !strings.Contains(err.Error(), "模型参数") {
		t.Fatalf("parameter mismatch: %v", err)
	}
}

func writeTestSettings(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
