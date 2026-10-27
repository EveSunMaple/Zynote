package api

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const msgMethodHeaderUnsupported = "方法文件头部格式不支持"

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var skillFrontMatter = regexp.MustCompile(`(?s)^\x{FEFF}?---\r?\n(.*?)\r?\n---\r?\n(.+)$`)

func parseSkillValue(value string) (string, error) {
	if strings.HasPrefix(value, `"`) {
		var decoded string
		if json.Unmarshal([]byte(value), &decoded) != nil {
			return "", contract.InputInvalid("方法头部引号无效")
		}
		return decoded, nil
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	for _, prefix := range []string{"!", "&", "*", "[", "{", ">", "|"} {
		if strings.HasPrefix(value, prefix) {
			return "", contract.InputInvalid("方法头部仅支持普通文本")
		}
	}
	return value, nil
}

func parseSkillFields(raw string) (map[string]string, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		index := strings.Index(line, ":")
		if index <= 0 {
			return nil, contract.InputInvalid(msgMethodHeaderUnsupported)
		}
		key := strings.TrimSpace(line[:index])
		if key != "name" && key != "description" {
			return nil, contract.InputInvalid(msgMethodHeaderUnsupported)
		}
		if _, exists := fields[key]; exists {
			return nil, contract.InputInvalid(msgMethodHeaderUnsupported)
		}
		value, err := parseSkillValue(strings.TrimSpace(line[index+1:]))
		if err != nil {
			return nil, err
		}
		fields[key] = value
	}
	return fields, nil
}

func invalidSkillCharacters(text string) bool {
	for _, char := range text {
		if char >= 0 && char <= 8 || char == 11 || char == 12 || (char >= 14 && char <= 31) {
			return true
		}
	}
	return false
}

func parseSkillMarkdown(text string) (map[string]any, error) {
	if len(text) > 12000 {
		return nil, contract.InputInvalid("方法文件超过 12000 字，请精简后导入")
	}
	match := skillFrontMatter.FindStringSubmatch(text)
	if match == nil {
		return nil, contract.InputInvalid("方法文件需包含 name 和 description 的头部及正文")
	}
	fields, err := parseSkillFields(match[1])
	if err != nil {
		return nil, err
	}
	name := fields["name"]
	description := fields["description"]
	body := strings.TrimSpace(match[2])
	if !skillNamePattern.MatchString(name) || len([]rune(name)) > 64 ||
		len([]rune(description)) < 1 || len([]rune(description)) > 500 ||
		len([]rune(body)) < 1 || len([]rune(body)) > 6000 {
		return nil, contract.InputInvalid(msgMethodHeaderUnsupported)
	}
	if invalidSkillCharacters(text) {
		return nil, contract.InputInvalid("学习方法包含无效字符")
	}
	return map[string]any{
		"name": name, "description": description, "body": body, "enabled": false,
	}, nil
}

func (inv *Invoker) handleSkillOpen(store contract.StateStore, state *contract.State) (any, error) {
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	raw, err := reverse.Call("shell.pickFile", map[string]any{
		"title": "导入学习方法", "extensions": []string{"md"}, "defaultPath": state.LibraryRoot,
	}, 600000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Path *string `json:"path"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("文件选择器响应无效")
	}
	if picked.Path == nil || *picked.Path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(*picked.Path)
	if err != nil {
		return nil, contract.InputInvalid("方法文件不可读")
	}
	return parseSkillMarkdown(string(body))
}
