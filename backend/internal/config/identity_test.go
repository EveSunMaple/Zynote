package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIdentity(t *testing.T, root string, value Identity) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "identity.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityDefaultsAreNeutral(t *testing.T) {
	value, err := ReadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if value.UserName != "我" || value.AssistantName != "Qwen" || value.Persona != defaultPersona {
		t.Fatalf("unexpected defaults: %+v", value)
	}
}

func TestIdentityMigratesLegacyDefaults(t *testing.T) {
	root := t.TempDir()
	writeIdentity(t, root, Identity{
		UserName:      "连酱",
		AssistantName: "Zynote",
		Persona:       legacyDefaultPersona,
	})
	value, err := ReadIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if value.UserName != "我" || value.AssistantName != "Qwen" || value.Persona != defaultPersona {
		t.Fatalf("legacy defaults were not migrated: %+v", value)
	}
}

func TestIdentityKeepsCustomValues(t *testing.T) {
	root := t.TempDir()
	writeIdentity(t, root, Identity{
		UserName:      "小云",
		AssistantName: "小助手",
		Persona:       "自定义人格",
	})
	value, err := ReadIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if value.UserName != "小云" || value.AssistantName != "小助手" || value.Persona != "自定义人格" {
		t.Fatalf("custom identity changed: %+v", value)
	}
}

func TestIdentityPromptFallbackIsNeutral(t *testing.T) {
	prompt := IdentityPrompt(Identity{UserName: "我", AssistantName: "Qwen"})
	if !strings.Contains(prompt, "你是 Qwen") || strings.Contains(prompt, "连酱") || strings.Contains(prompt, "Zynote") {
		t.Fatalf("unexpected fallback prompt: %s", prompt)
	}
}
