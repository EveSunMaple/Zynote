package config

import (
	"encoding/json"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type settingsSaveProfile struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	BaseURL     string                     `json:"baseUrl"`
	Model       string                     `json:"model"`
	VisionModel string                     `json:"visionModel"`
	Provider    string                     `json:"provider"`
	APIFormat   string                     `json:"apiFormat"`
	APIKey      string                     `json:"apiKey"`
	Parameters  map[string]ModelParameters `json:"parameters"`
	Models      []string                   `json:"models"`
}

type settingsSaveInput struct {
	Profiles       []settingsSaveProfile `json:"profiles"`
	ActiveProfile  string                `json:"activeProfile"`
	SearchKey      json.RawMessage       `json:"searchKey"`
	SearchProvider string                `json:"searchProvider"`
	SearchEndpoint string                `json:"searchEndpoint"`
	Theme          string                `json:"theme"`
	BrowserWidth   float64               `json:"browserWidth"`
	BrowserOpen    bool                  `json:"browserOpen"`
	MaxMinutes     float64               `json:"maxMinutes"`
	MaxGB          float64               `json:"maxGB"`
	Compress       bool                  `json:"compress"`
	Prompts        map[string]string     `json:"prompts"`
}

type SettingsSaveResult struct {
	Settings          map[string]any
	Connection        string
	Model             string
	ConnectionChanged bool
}

func uniqueSettingsStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func encryptValue(reverse contract.ReverseCaller, value string) (string, error) {
	if reverse == nil {
		return "", contract.Unavailable("系统加密存储不可用，未保存设置")
	}
	raw, err := reverse.Call("crypto.protect", map[string]string{"value": value}, unprotectTimeoutMS)
	if err != nil {
		return "", contract.Unavailable("系统加密存储不可用，未保存设置")
	}
	var decoded struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Value == "" {
		return "", contract.Unavailable("系统加密存储不可用，未保存设置")
	}
	return decoded.Value, nil
}

func sameParameters(left, right map[string]ModelParameters) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return reflect.DeepEqual(left, right)
}

func settingsProfileChanged(old StoredProfile, input settingsSaveProfile, name, baseURL, model, vision string, apiKey string) bool {
	return old.Name != name || strings.TrimRight(old.BaseURL, "/") != baseURL ||
		old.Model != model || old.VisionModel != vision ||
		!sameParameters(old.Parameters, input.Parameters) || apiKey != ""
}

func SaveSettings(
	dataRoot string,
	promptsRoot string,
	reverse contract.ReverseCaller,
	raw json.RawMessage,
	busy bool,
) (SettingsSaveResult, error) {
	var input settingsSaveInput
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return SettingsSaveResult{}, contract.InputInvalid("设置参数无效")
	}
	if input.MaxMinutes < 1 || input.MaxMinutes > 60 || !(input.MaxGB > 0) || input.MaxGB > 10 {
		return SettingsSaveResult{}, contract.InputInvalid("视频限制设置不合法")
	}
	chosen := settingsSaveProfile{
		ID: "qwen", Name: defaultProfile.Name, BaseURL: defaultProfile.BaseURL,
		Model: defaultProfile.Model, VisionModel: defaultProfile.VisionModel,
		APIFormat: "chat_completions", Provider: "qwen",
	}
	if len(input.Profiles) > 0 {
		chosen = input.Profiles[0]
		for _, profile := range input.Profiles {
			if profile.ID == input.ActiveProfile {
				chosen = profile
				break
			}
		}
	}
	name := strings.TrimSpace(chosen.Name)
	model := strings.TrimSpace(chosen.Model)
	vision := strings.TrimSpace(chosen.VisionModel)
	baseURL := strings.TrimSpace(chosen.BaseURL)
	if name == "" {
		return SettingsSaveResult{}, contract.InputInvalid("请填写配置名称")
	}
	if baseURL == "" {
		return SettingsSaveResult{}, contract.InputInvalid("请填写千问 API 的 Base URL")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return SettingsSaveResult{}, contract.InputInvalid("API 地址格式不正确，请填写完整地址")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return SettingsSaveResult{}, contract.InputInvalid("API 地址必须是 HTTP(S)，不能包含密码")
	}
	identity, err := endpointIdentity(baseURL)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	for _, parameter := range chosen.Parameters {
		if parameter.Temperature != nil &&
			(math.IsNaN(*parameter.Temperature) || *parameter.Temperature < 0 || *parameter.Temperature > 2) {
			return SettingsSaveResult{}, contract.InputInvalid("模型参数超出允许范围")
		}
		if parameter.TopP != nil && (math.IsNaN(*parameter.TopP) || *parameter.TopP < 0 || *parameter.TopP > 1) {
			return SettingsSaveResult{}, contract.InputInvalid("模型参数超出允许范围")
		}
		if parameter.MaxTokens != nil && *parameter.MaxTokens < 1 {
			return SettingsSaveResult{}, contract.InputInvalid("模型参数超出允许范围")
		}
	}
	stored, err := loadStored(dataRoot)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	old := defaultProfile
	if rawProfile, ok := stored["qwenProfile"]; ok {
		if err := json.Unmarshal(rawProfile, &old); err != nil {
			return SettingsSaveResult{}, contract.InputInvalid("模型配置无效")
		}
	}
	oldIdentity := ""
	if old.BaseURL != "" {
		oldIdentity, _ = endpointIdentity(old.BaseURL)
	}
	apiKey := strings.TrimSpace(chosen.APIKey)
	if apiKey == "" && old.EncryptedKey != "" && identity != oldIdentity {
		return SettingsSaveResult{}, contract.InputInvalid(
			"API 地址已改变。为避免把旧密钥发送到新地址，请重新填写该地址的千问 API Key",
		)
	}
	profileChanged := settingsProfileChanged(old, chosen, name, strings.TrimRight(baseURL, "/"), model, vision, apiKey)
	if busy && profileChanged {
		return SettingsSaveResult{}, contract.InputInvalid("任务运行中，请完成或取消后再更换模型连接")
	}
	if apiKey == "" && old.EncryptedKey != "" {
		if _, err := decryptValue(reverse, old.EncryptedKey); err != nil {
			return SettingsSaveResult{}, contract.Unavailable("系统加密存储不可用，未保存设置")
		}
	}
	keepSearch := len(input.SearchKey) == 0
	clearSearch := false
	searchValue := ""
	if !keepSearch {
		if string(input.SearchKey) == "null" {
			clearSearch = true
		} else if err := json.Unmarshal(input.SearchKey, &searchValue); err != nil {
			return SettingsSaveResult{}, contract.InputInvalid("搜索密钥无效")
		} else if searchValue == "" {
			clearSearch = true
		}
	}
	oldSearchKey := rawString(stored["encryptedSearchKey"])
	if keepSearch && oldSearchKey != "" {
		if _, err := decryptValue(reverse, oldSearchKey); err != nil {
			return SettingsSaveResult{}, contract.Unavailable("系统加密存储不可用，未保存设置")
		}
	}
	encryptedKey := old.EncryptedKey
	if apiKey != "" {
		encryptedKey, err = encryptValue(reverse, apiKey)
		if err != nil {
			return SettingsSaveResult{}, err
		}
	}
	encryptedSearchKey := oldSearchKey
	if clearSearch {
		encryptedSearchKey = ""
	} else if !keepSearch {
		encryptedSearchKey, err = encryptValue(reverse, searchValue)
		if err != nil {
			return SettingsSaveResult{}, err
		}
	}
	parameters := chosen.Parameters
	if parameters == nil {
		parameters = map[string]ModelParameters{}
	}
	profile := StoredProfile{
		ID: "qwen", Name: name, BaseURL: strings.TrimRight(baseURL, "/"), Model: model,
		VisionModel: vision, APIFormat: "chat_completions", Provider: "qwen",
		Parameters:   parameters,
		Models:       uniqueSettingsStrings(append([]string{model, vision}, chosen.Models...)),
		EncryptedKey: encryptedKey,
	}
	theme := "light"
	if input.Theme == "dark" || input.Theme == "system" {
		theme = input.Theme
	}
	width := math.Min(900, math.Max(280, input.BrowserWidth))
	provider := "duckduckgo"
	if input.SearchProvider == "searxng" {
		provider = "searxng"
	}
	next := map[string]json.RawMessage{}
	for key, value := range stored {
		next[key] = value
	}
	set := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		next[key] = encoded
		return nil
	}
	prompts := input.Prompts
	if prompts == nil {
		prompts = map[string]string{}
	}
	for _, item := range []struct {
		key   string
		value any
	}{
		{"connection", "qwen"}, {"qwenProfile", profile}, {"searchProvider", provider},
		{"searchEndpoint", strings.TrimSpace(input.SearchEndpoint)}, {"videoPolicyVersion", 2},
		{"theme", theme}, {"browserWidth", width}, {"browserOpen", input.BrowserOpen},
		{"maxMinutes", input.MaxMinutes}, {"maxGB", input.MaxGB}, {"compress", input.Compress},
		{"prompts", prompts},
	} {
		if err := set(item.key, item.value); err != nil {
			return SettingsSaveResult{}, contract.Unavailable("设置文件不可写")
		}
	}
	if encryptedSearchKey == "" {
		delete(next, "encryptedSearchKey")
	} else if err := set("encryptedSearchKey", encryptedSearchKey); err != nil {
		return SettingsSaveResult{}, contract.Unavailable("设置文件不可写")
	}
	encoded, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return SettingsSaveResult{}, contract.Unavailable("设置文件不可写")
	}
	path := filepath.Join(dataRoot, "settings.json")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return SettingsSaveResult{}, contract.Unavailable("设置文件不可写")
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return SettingsSaveResult{}, contract.Unavailable("设置文件不可写")
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return SettingsSaveResult{}, contract.Unavailable("设置文件不可写")
	}
	saved, err := ReadSettings(dataRoot, promptsRoot, reverse)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	return SettingsSaveResult{
		Settings: saved, Connection: connectionFingerprint(profile), Model: model,
		ConnectionChanged: connectionFingerprint(profile) != connectionFingerprint(old),
	}, nil
}
