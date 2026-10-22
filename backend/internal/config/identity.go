package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type Identity struct {
	UserName        string `json:"userName"`
	AssistantName   string `json:"assistantName"`
	UserAvatar      string `json:"userAvatar"`
	AssistantAvatar string `json:"assistantAvatar"`
	Persona         string `json:"persona"`
}

const defaultPersona = "你是 Zynote，连酱的学习伙伴。你反应快、靠谱，带着一点幽默感，偶尔会俏皮地回嘴，但分寸得当；主动、自信，尊重彼此的意愿和边界。日常对话自然、亲切，不过度表演，不重复口头禅；陪连酱学习时认真清楚：先回答问题，再按需要解释概念、举例和梳理重点，区分老师原话、资料内容和自己的推断，不因角色互动编造知识或答案。角色互动保持日常、友善，不使用露骨、性化或强迫性的描写。"

var DefaultIdentity = Identity{
	UserName:        "连酱",
	AssistantName:   "Zynote",
	UserAvatar:      "",
	AssistantAvatar: "",
	Persona:         defaultPersona,
}

func ReadIdentity(dataRoot string) (Identity, error) {
	path := filepath.Join(dataRoot, "identity.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultIdentity, nil
		}
		return Identity{}, contract.Unavailable("身份文件不可读")
	}
	var value Identity
	if err := json.Unmarshal(raw, &value); err != nil {
		return Identity{}, contract.InputInvalid("身份文件格式无效")
	}
	if len(value.UserName) < 1 || len(value.UserName) > 40 ||
		len(value.AssistantName) < 1 || len(value.AssistantName) > 40 || len(value.Persona) > 12000 ||
		!validAvatar(value.UserAvatar) || !validAvatar(value.AssistantAvatar) {
		return Identity{}, contract.InputInvalid("身份文件字段无效")
	}
	if strings.TrimSpace(value.Persona) == "" {
		value.Persona = DefaultIdentity.Persona
	}
	if value.UserName == "你" {
		value.UserName = DefaultIdentity.UserName
	}
	if value.AssistantName == "听课助手" || value.AssistantName == "梅酱" {
		value.AssistantName = DefaultIdentity.AssistantName
	}
	return value, nil
}

func validAvatar(value string) bool {
	return value == "" || (len(value) <= 1000000 && strings.HasPrefix(value, "data:image/png;base64,"))
}
