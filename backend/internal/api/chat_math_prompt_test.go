package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
)

func TestChatMathPresentationAcrossPromptSources(t *testing.T) {
	for _, source := range []string{"fallback", "resource", "custom"} {
		t.Run(source, func(t *testing.T) {
			dataRoot, resourcesRoot := t.TempDir(), t.TempDir()
			settings := map[string]any{"prompts": map[string]string{}}
			expected := defaultChatPrompt
			if source != "fallback" {
				if err := os.MkdirAll(filepath.Join(resourcesRoot, "prompts"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(resourcesRoot, "prompts", "chat.txt"), []byte("资源提示词：保留来源要求。"), 0o600); err != nil {
					t.Fatal(err)
				}
				expected = "资源提示词：保留来源要求。"
			}
			if source == "custom" {
				expected = "用户自定义提示词：保持我的称呼和引用规则。"
				settings["prompts"] = map[string]string{"chat": expected}
			}
			raw, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dataRoot, "settings.json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			prompt, err := config.Prompt(dataRoot, resourcesRoot, "chat")
			if err != nil {
				t.Fatal(err)
			}
			identity := config.Identity{UserName: "测试用户", AssistantName: "测试助手", Persona: "保留角色原文"}
			result := chatSystemPrompt(identity, prompt, chatLectureContext{Course: "数学课程", Transcript: "[00:01] 引用原文"})
			for _, preserved := range []string{expected, config.IdentityPrompt(identity), "[00:01] 引用原文", "不要用常识冒充课堂内容"} {
				if !strings.Contains(result, preserved) {
					t.Fatalf("missing preserved content %q", preserved)
				}
			}
			if !strings.HasSuffix(result, chatMathPresentation) {
				t.Fatal("math presentation contract missing at common prompt exit")
			}
			if !strings.Contains(result, `\(...\)`) || !strings.Contains(result, `\[...\]`) ||
				strings.Contains(chatMathPresentation, `\\(...\\)`) || strings.Contains(chatMathPresentation, `\\[...\\]`) {
				t.Fatal("math examples must use single runtime LaTeX backslashes")
			}
		})
	}
}
