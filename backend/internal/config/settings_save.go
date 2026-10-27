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

const (
	errSecureStorageUnavailable  = "系统加密存储不可用，未保存设置"
	errModelParametersOutOfRange = "模型参数超出允许范围"
	errSettingsFileNotWritable   = "设置文件不可写"
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
		return "", contract.Unavailable(errSecureStorageUnavailable)
	}
	raw, err := reverse.Call("crypto.protect", map[string]string{"value": value}, unprotectTimeoutMS)
	if err != nil {
		return "", contract.Unavailable(errSecureStorageUnavailable)
	}
	var decoded struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Value == "" {
		return "", contract.Unavailable(errSecureStorageUnavailable)
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

func parseSettingsInput(raw json.RawMessage) (settingsSaveInput, error) {
	var input settingsSaveInput
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return input, contract.InputInvalid("设置参数无效")
	}
	if input.MaxMinutes < 1 || input.MaxMinutes > 60 || !(input.MaxGB > 0) || input.MaxGB > 10 {
		return input, contract.InputInvalid("视频限制设置不合法")
	}
	return input, nil
}

func chooseSettingsProfile(input settingsSaveInput) settingsSaveProfile {
	chosen := settingsSaveProfile{
		ID: "qwen", Name: defaultProfile.Name, BaseURL: defaultProfile.BaseURL,
		Model: defaultProfile.Model, VisionModel: defaultProfile.VisionModel,
		APIFormat: "chat_completions", Provider: "qwen",
	}
	if len(input.Profiles) == 0 {
		return chosen
	}
	chosen = input.Profiles[0]
	for _, profile := range input.Profiles {
		if profile.ID == input.ActiveProfile {
			return profile
		}
	}
	return chosen
}

func validateSettingsProfile(chosen settingsSaveProfile) (string, string, string, string, string, error) {
	name := strings.TrimSpace(chosen.Name)
	model := strings.TrimSpace(chosen.Model)
	vision := strings.TrimSpace(chosen.VisionModel)
	baseURL := strings.TrimSpace(chosen.BaseURL)
	if name == "" {
		return "", "", "", "", "", contract.InputInvalid("请填写配置名称")
	}
	if baseURL == "" {
		return "", "", "", "", "", contract.InputInvalid("请填写千问 API 的 Base URL")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", "", "", "", contract.InputInvalid("API 地址格式不正确，请填写完整地址")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", "", "", "", "", contract.InputInvalid("API 地址必须是 HTTP(S)，不能包含密码")
	}
	identity, err := endpointIdentity(baseURL)
	if err != nil {
		return "", "", "", "", "", err
	}
	for _, parameter := range chosen.Parameters {
		if parameter.Temperature != nil &&
			(math.IsNaN(*parameter.Temperature) || *parameter.Temperature < 0 || *parameter.Temperature > 2) {
			return "", "", "", "", "", contract.InputInvalid(errModelParametersOutOfRange)
		}
		if parameter.TopP != nil && (math.IsNaN(*parameter.TopP) || *parameter.TopP < 0 || *parameter.TopP > 1) {
			return "", "", "", "", "", contract.InputInvalid(errModelParametersOutOfRange)
		}
		if parameter.MaxTokens != nil && *parameter.MaxTokens < 1 {
			return "", "", "", "", "", contract.InputInvalid(errModelParametersOutOfRange)
		}
	}
	return name, baseURL, model, vision, identity, nil
}

func loadSettingsState(dataRoot string) (map[string]json.RawMessage, StoredProfile, string, error) {
	stored, err := loadStored(dataRoot)
	if err != nil {
		return nil, StoredProfile{}, "", err
	}
	old := defaultProfile
	if rawProfile, ok := stored["qwenProfile"]; ok {
		if err := json.Unmarshal(rawProfile, &old); err != nil {
			return nil, StoredProfile{}, "", contract.InputInvalid("模型配置无效")
		}
	}
	oldIdentity := ""
	if old.BaseURL != "" {
		oldIdentity, _ = endpointIdentity(old.BaseURL)
	}
	return stored, old, oldIdentity, nil
}

func resolveSettingsAPIKey(reverse contract.ReverseCaller, old StoredProfile, identity string, chosen settingsSaveProfile, busy bool, name, baseURL, model, vision string) (string, string, bool, error) {
	apiKey := strings.TrimSpace(chosen.APIKey)
	oldIdentity := ""
	if old.BaseURL != "" {
		oldIdentity, _ = endpointIdentity(old.BaseURL)
	}
	if apiKey == "" && old.EncryptedKey != "" && identity != oldIdentity {
		return "", "", false, contract.InputInvalid(
			"API 地址已改变。为避免把旧密钥发送到新地址，请重新填写该地址的千问 API Key",
		)
	}
	changed := settingsProfileChanged(old, chosen, name, strings.TrimRight(baseURL, "/"), model, vision, apiKey)
	if busy && changed {
		return "", "", false, contract.InputInvalid("任务运行中，请完成或取消后再更换模型连接")
	}
	if apiKey == "" && old.EncryptedKey != "" {
		if _, err := decryptValue(reverse, old.EncryptedKey); err != nil {
			return "", "", false, contract.Unavailable(errSecureStorageUnavailable)
		}
	}
	encrypted := old.EncryptedKey
	if apiKey != "" {
		value, err := encryptValue(reverse, apiKey)
		if err != nil {
			return "", "", false, err
		}
		encrypted = value
	}
	return apiKey, encrypted, changed, nil
}

func resolveSettingsSearchKey(reverse contract.ReverseCaller, input settingsSaveInput, stored map[string]json.RawMessage) (string, error) {
	keep := len(input.SearchKey) == 0
	clear := false
	searchValue := ""
	if !keep {
		if string(input.SearchKey) == "null" {
			clear = true
		} else if err := json.Unmarshal(input.SearchKey, &searchValue); err != nil {
			return "", contract.InputInvalid("搜索密钥无效")
		} else if searchValue == "" {
			clear = true
		}
	}
	oldSearchKey := rawString(stored["encryptedSearchKey"])
	if keep && oldSearchKey != "" {
		if _, err := decryptValue(reverse, oldSearchKey); err != nil {
			return "", contract.Unavailable(errSecureStorageUnavailable)
		}
	}
	if clear {
		return "", nil
	}
	if !keep {
		return encryptValue(reverse, searchValue)
	}
	return oldSearchKey, nil
}

func storedSettingsProfile(chosen settingsSaveProfile, name, baseURL, model, vision, encryptedKey string) StoredProfile {
	parameters := chosen.Parameters
	if parameters == nil {
		parameters = map[string]ModelParameters{}
	}
	return StoredProfile{
		ID: "qwen", Name: name, BaseURL: strings.TrimRight(baseURL, "/"), Model: model,
		VisionModel: vision, APIFormat: "chat_completions", Provider: "qwen",
		Parameters:   parameters,
		Models:       uniqueSettingsStrings(append([]string{model, vision}, chosen.Models...)),
		EncryptedKey: encryptedKey,
	}
}

func buildSettingsMap(stored map[string]json.RawMessage, input settingsSaveInput, profile StoredProfile, encryptedSearchKey string) (map[string]json.RawMessage, error) {
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
			return nil, contract.Unavailable(errSettingsFileNotWritable)
		}
	}
	if encryptedSearchKey == "" {
		delete(next, "encryptedSearchKey")
	} else if err := set("encryptedSearchKey", encryptedSearchKey); err != nil {
		return nil, contract.Unavailable(errSettingsFileNotWritable)
	}
	return next, nil
}

func writeSettingsFile(dataRoot string, next map[string]json.RawMessage) error {
	encoded, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return contract.Unavailable(errSettingsFileNotWritable)
	}
	path := filepath.Join(dataRoot, "settings.json")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return contract.Unavailable(errSettingsFileNotWritable)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return contract.Unavailable(errSettingsFileNotWritable)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return contract.Unavailable(errSettingsFileNotWritable)
	}
	return nil
}

func SaveSettings(
	dataRoot string,
	promptsRoot string,
	reverse contract.ReverseCaller,
	raw json.RawMessage,
	busy bool,
) (SettingsSaveResult, error) {
	input, err := parseSettingsInput(raw)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	chosen := chooseSettingsProfile(input)
	name, baseURL, model, vision, identity, err := validateSettingsProfile(chosen)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	stored, old, _, err := loadSettingsState(dataRoot)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	_, encryptedKey, _, err := resolveSettingsAPIKey(reverse, old, identity, chosen, busy, name, baseURL, model, vision)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	encryptedSearchKey, err := resolveSettingsSearchKey(reverse, input, stored)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	profile := storedSettingsProfile(chosen, name, baseURL, model, vision, encryptedKey)
	next, err := buildSettingsMap(stored, input, profile, encryptedSearchKey)
	if err != nil {
		return SettingsSaveResult{}, err
	}
	if err := writeSettingsFile(dataRoot, next); err != nil {
		return SettingsSaveResult{}, err
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
