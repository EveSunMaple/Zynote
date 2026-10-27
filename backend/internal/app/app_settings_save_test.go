package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsSaveInput(profile map[string]any, extra map[string]any) map[string]any { // NOSONAR: path handling matches the platform API contract.
	value := map[string]any{
		"profiles": []any{profile}, "activeProfile": "qwen", "theme": "dark",
		"browserWidth": 1000, "browserOpen": false, "maxMinutes": 45, "maxGB": 2.5,
		"compress": true, "searchProvider": "searxng", "searchEndpoint": " https://search.example ",
		"prompts": map[string]string{"notes": "自定义笔记"},
	}
	for key, item := range extra {
		value[key] = item
	}
	return value
}

func TestSettingsSave(t *testing.T) {
	dataRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"legacy":"keep","qwenProfile":{"id":"qwen","name":"旧配置","baseUrl":"https://api.example/v1","model":"m1","visionModel":"","apiFormat":"chat_completions","provider":"qwen","parameters":{},"encryptedKey":"old-cipher"}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": dataRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	profile := map[string]any{
		"id": "qwen", "name": "新配置", "baseUrl": "https://api.example/v1/", "model": "m2",
		"visionModel": "v1", "provider": "qwen", "apiFormat": "chat_completions",
		"apiKey":     " new-key ",
		"parameters": map[string]any{"m2": map[string]any{"maxTokens": 512}},
	}
	raw, errReply := f.call("settings:save", map[string]any{
		"settings": settingsSaveInput(profile, map[string]any{"searchKey": "tavily-key"}),
	})
	if errReply != nil {
		t.Fatalf("settings:save failed: %+v", errReply)
	}
	var saved struct {
		HasSearchKey bool   `json:"hasSearchKey"`
		Theme        string `json:"theme"`
		MaxMinutes   int    `json:"maxMinutes"`
		Profiles     []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			HasKey bool   `json:"hasKey"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.HasSearchKey || saved.Theme != "dark" || saved.MaxMinutes != 45 ||
		len(saved.Profiles) != 1 || saved.Profiles[0].Name != "新配置" ||
		saved.Profiles[0].Model != "m2" || !saved.Profiles[0].HasKey {
		t.Fatalf("saved settings mismatch: %s", raw)
	}
	fileRaw, err := os.ReadFile(filepath.Join(dataRoot, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(fileRaw, &file); err != nil {
		t.Fatal(err)
	}
	if file["legacy"] != "keep" || file["theme"] != "dark" || file["browserWidth"] != float64(900) ||
		file["searchEndpoint"] != "https://search.example" {
		t.Fatalf("file merge mismatch: %s", fileRaw)
	}
	profileFile := file["qwenProfile"].(map[string]any)
	if profileFile["encryptedKey"] == "new-key" || profileFile["name"] != "新配置" ||
		profileFile["baseUrl"] != "https://api.example/v1" {
		t.Fatalf("credential or profile mismatch: %s", fileRaw)
	}
	if strings.Contains(string(fileRaw), "new-key") || strings.Contains(string(fileRaw), "tavily-key") {
		t.Fatalf("plaintext credentials persisted: %s", fileRaw)
	}
	second := settingsSaveInput(map[string]any{
		"id": "qwen", "name": "新配置", "baseUrl": "https://api.example/v1", "model": "m2",
		"visionModel": "v1", "parameters": map[string]any{"m2": map[string]any{"maxTokens": 512}},
	}, nil)
	if _, errReply := f.call("settings:save", map[string]any{"settings": second}); errReply != nil {
		t.Fatalf("retained credentials save failed: %+v", errReply)
	}
	fileRaw, _ = os.ReadFile(filepath.Join(dataRoot, "settings.json"))
	_ = json.Unmarshal(fileRaw, &file)
	profileFile = file["qwenProfile"].(map[string]any)
	if profileFile["encryptedKey"] == "" || file["encryptedSearchKey"] == nil {
		t.Fatalf("credentials should be retained: %s", fileRaw)
	}
	changedAddress := settingsSaveInput(map[string]any{
		"id": "qwen", "name": "新配置", "baseUrl": "https://other.example/v1", "model": "m2",
		"visionModel": "v1", "parameters": map[string]any{"m2": map[string]any{"maxTokens": 512}},
	}, nil)
	if _, errReply := f.call("settings:save", map[string]any{"settings": changedAddress}); errReply == nil ||
		!strings.Contains(errReply.Message, "API 地址已改变") {
		t.Fatalf("address guard mismatch: %+v", errReply)
	}
}
