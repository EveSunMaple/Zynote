package config

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type ModelParameters struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxTokens       *int     `json:"maxTokens,omitempty"`
	ReasoningEffort *string  `json:"reasoningEffort,omitempty"`
}

type StoredProfile struct {
	ID           string                     `json:"id"`
	Name         string                     `json:"name"`
	BaseURL      string                     `json:"baseUrl"`
	Model        string                     `json:"model"`
	VisionModel  string                     `json:"visionModel"`
	APIFormat    string                     `json:"apiFormat"`
	Provider     string                     `json:"provider"`
	Parameters   map[string]ModelParameters `json:"parameters"`
	Models       []string                   `json:"models"`
	EncryptedKey string                     `json:"encryptedKey,omitempty"`
	APIKey       string                     `json:"apiKey,omitempty"`
}

var defaultProfile = StoredProfile{
	ID:          "qwen-default",
	Name:        "通义千问",
	Provider:    "qwen",
	BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
	Model:       "",
	VisionModel: "",
	APIFormat:   "chat_completions",
	Models:      []string{},
}

var promptNames = []string{"correction", "notes", "past-exam", "match", "search", "answer", "chat"}

const unprotectTimeoutMS = 5000

type storedSettings map[string]json.RawMessage

func ReadSettings(dataRoot, promptsRoot string, reverse contract.ReverseCaller) (map[string]any, error) {
	stored, err := loadStored(dataRoot)
	if err != nil {
		return nil, err
	}
	qwen := defaultProfile
	if raw, ok := stored["qwenProfile"]; ok {
		if err := json.Unmarshal(raw, &qwen); err != nil {
			return nil, contract.InputInvalid("模型配置无效")
		}
	}
	encryptedSearchKey := rawString(stored["encryptedSearchKey"])
	output := map[string]any{}
	for key, value := range stored {
		switch key {
		case "encryptedSearchKey", "profiles", "activeProfile", "connection", "codex", "qwenProfile", "videoPolicyVersion":
			continue
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, contract.InputInvalid("设置字段无效: " + key)
		}
		output[key] = decoded
	}
	prompts, err := readPrompts(stored, promptsRoot)
	if err != nil {
		return nil, err
	}
	output["connection"] = "qwen"
	output["hasSearchKey"] = encryptedSearchKey != ""
	output["activeProfile"] = qwen.ID
	output["prompts"] = prompts
	output["profiles"] = []map[string]any{publicProfile(qwen, reverse)}
	return output, nil
}

func loadStored(dataRoot string) (storedSettings, error) {
	path := filepath.Join(dataRoot, "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fallback := map[string]json.RawMessage{}
			defaults := map[string]any{
				"profiles": []any{}, "activeProfile": "", "theme": "light",
				"browserWidth": 420, "browserOpen": true, "maxMinutes": 60,
				"maxGB": 1.5, "compress": false, "prompts": map[string]string{},
			}
			for key, value := range defaults {
				encoded, marshalErr := json.Marshal(value)
				if marshalErr != nil {
					return nil, marshalErr
				}
				fallback[key] = encoded
			}
			return fallback, nil
		}
		return nil, contract.Unavailable("设置文件不可读")
	}
	var stored storedSettings
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, contract.InputInvalid("设置文件格式无效")
	}
	return stored, nil
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func readPrompts(stored storedSettings, promptsRoot string) (map[string]string, error) {
	existing := map[string]string{}
	if raw, ok := stored["prompts"]; ok {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return nil, contract.InputInvalid("提示词配置无效")
		}
	}
	result := map[string]string{}
	for _, name := range promptNames {
		if value, ok := existing[name]; ok {
			result[name] = value
			continue
		}
		raw, err := os.ReadFile(filepath.Join(promptsRoot, "prompts", name+".txt"))
		if err != nil {
			continue
		}
		result[name] = string(raw)
	}
	return result, nil
}

func publicProfile(profile StoredProfile, reverse contract.ReverseCaller) map[string]any {
	public := map[string]any{}
	if raw, err := json.Marshal(profile); err == nil {
		if err := json.Unmarshal(raw, &public); err != nil {
			public = map[string]any{}
		}
	}
	delete(public, "encryptedKey")
	delete(public, "apiKey")
	if profile.Parameters == nil {
		delete(public, "parameters")
	}
	if profile.Models == nil {
		delete(public, "models")
	}
	public["provider"] = "qwen"
	public["apiFormat"] = "chat_completions"
	keyLength := 0
	if profile.EncryptedKey != "" && reverse != nil {
		if plain, err := decryptValue(reverse, profile.EncryptedKey); err == nil {
			keyLength = len([]rune(plain))
		}
	}
	public["hasKey"] = profile.EncryptedKey != ""
	public["keyLength"] = keyLength
	return public
}

func decryptValue(reverse contract.ReverseCaller, value string) (string, error) {
	raw, err := reverse.Call("crypto.unprotect", map[string]string{"value": value}, unprotectTimeoutMS)
	if err != nil {
		return "", err
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	return decoded.Text, nil
}

func IsUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
				return false
			}
		}
	}
	return true
}

type RuntimeProfile struct {
	BaseURL     string
	Model       string
	VisionModel string
	APIFormat   string
	Key         string
	Parameters  map[string]ModelParameters
}

func ActiveProfile(dataRoot string, reverse contract.ReverseCaller) (RuntimeProfile, error) {
	stored, err := loadStored(dataRoot)
	if err != nil {
		return RuntimeProfile{}, err
	}
	profile := defaultProfile
	if raw, ok := stored["qwenProfile"]; ok {
		if err := json.Unmarshal(raw, &profile); err != nil {
			return RuntimeProfile{}, contract.InputInvalid("模型配置无效")
		}
	}
	baseURL := strings.TrimSpace(profile.BaseURL)
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return RuntimeProfile{}, contract.InputInvalid("请填写完整的 API 地址")
	}
	if profile.Provider != "" && profile.Provider != "qwen" {
		return RuntimeProfile{}, contract.InputInvalid("本版本仅支持千问模型配置")
	}
	key := ""
	if profile.EncryptedKey != "" && reverse != nil {
		if plain, decryptErr := decryptValue(reverse, profile.EncryptedKey); decryptErr == nil {
			key = plain
		}
	}
	if strings.TrimSpace(key) == "" {
		return RuntimeProfile{}, contract.InputInvalid("请填写千问 API Key，并确认 Base URL 与密钥属于同一服务")
	}
	return RuntimeProfile{
		BaseURL: strings.TrimRight(baseURL, "/"), Model: strings.TrimSpace(profile.Model),
		VisionModel: strings.TrimSpace(profile.VisionModel), APIFormat: "chat_completions",
		Key: key, Parameters: profile.Parameters,
	}, nil
}

type ProfileDraft struct {
	ID          string
	Name        string
	BaseURL     string
	Model       string
	VisionModel string
	Provider    string
	APIFormat   string
	APIKey      string
	Parameters  map[string]ModelParameters
}

func endpointIdentity(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Hostname() == "" {
		return "", contract.InputInvalid("请填写完整的 API 地址")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", contract.InputInvalid("API 地址不能包含查询参数或片段")
	}
	return parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.Path, "/"), nil
}

func DraftProfile(dataRoot string, reverse contract.ReverseCaller, draft ProfileDraft) (RuntimeProfile, error) {
	identity, err := endpointIdentity(draft.BaseURL)
	if err != nil {
		return RuntimeProfile{}, err
	}
	if draft.Provider != "" && draft.Provider != "qwen" {
		return RuntimeProfile{}, contract.InputInvalid("本版本仅支持千问模型配置")
	}
	stored, err := loadStored(dataRoot)
	if err != nil {
		return RuntimeProfile{}, err
	}
	profile := defaultProfile
	if raw, ok := stored["qwenProfile"]; ok {
		if err := json.Unmarshal(raw, &profile); err != nil {
			return RuntimeProfile{}, contract.InputInvalid("模型配置无效")
		}
	}
	storedIdentity := ""
	if strings.TrimSpace(profile.BaseURL) != "" {
		storedIdentity, _ = endpointIdentity(profile.BaseURL)
	}
	key := strings.TrimSpace(draft.APIKey)
	if key == "" && storedIdentity == identity && profile.EncryptedKey != "" && reverse != nil {
		if plain, decryptErr := decryptValue(reverse, profile.EncryptedKey); decryptErr == nil {
			key = plain
		}
	}
	if strings.TrimSpace(key) == "" {
		return RuntimeProfile{}, contract.InputInvalid("请填写千问 API Key，并确认 Base URL 与密钥属于同一服务")
	}
	return RuntimeProfile{
		BaseURL: identity, Model: strings.TrimSpace(draft.Model),
		VisionModel: strings.TrimSpace(draft.VisionModel), APIFormat: "chat_completions",
		Key: key, Parameters: draft.Parameters,
	}, nil
}

func CacheModels(dataRoot string, models []string) error {
	stored, err := loadStored(dataRoot)
	if err != nil {
		return err
	}
	raw, ok := stored["qwenProfile"]
	if !ok {
		return nil
	}
	profile := defaultProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return contract.InputInvalid("模型配置无效")
	}
	merged := []string{}
	seen := map[string]bool{}
	for _, value := range append([]string{profile.Model, profile.VisionModel}, models...) {
		if value != "" && !seen[value] {
			seen[value] = true
			merged = append(merged, value)
		}
	}
	if sameStrings(merged, profile.Models) {
		return nil
	}
	profile.Models = merged
	encoded, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	stored["qwenProfile"] = encoded
	file := map[string]json.RawMessage(stored)
	return writeAtomic(filepath.Join(dataRoot, "settings.json"), file)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func Prompt(dataRoot, promptsRoot, name string) (string, error) {
	stored, err := loadStored(dataRoot)
	if err != nil {
		return "", err
	}
	prompts, err := readPrompts(stored, promptsRoot)
	if err != nil {
		return "", err
	}
	return prompts[name], nil
}
