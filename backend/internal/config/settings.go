package config

import (
	"encoding/json"
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
	APIFormat    string                     `json:"apiFormat,omitempty"`
	Provider     string                     `json:"provider,omitempty"`
	Parameters   map[string]ModelParameters `json:"parameters,omitempty"`
	Models       []string                   `json:"models,omitempty"`
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

func ReadSettings(dataRoot, resourcesRoot string, reverse contract.ReverseCaller) (map[string]any, error) {
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
	prompts, err := readPrompts(stored, resourcesRoot)
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

func readPrompts(stored storedSettings, resourcesRoot string) (map[string]string, error) {
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
		raw, err := os.ReadFile(filepath.Join(resourcesRoot, "prompts", name+".txt"))
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

func isUUID(value string) bool {
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
