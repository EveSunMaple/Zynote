package config

import "testing"

func TestConnectionFingerprintParityWithTypeScript(t *testing.T) {
	temperature := 0.2
	topP := 0.9
	maxTokens := 250
	profile := StoredProfile{
		BaseURL: "https://api.example/v1", Model: "text", VisionModel: "vision",
		APIFormat: "chat_completions", EncryptedKey: "encrypted",
		Parameters: map[string]ModelParameters{
			"text": {Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens},
		},
	}
	if value := connectionFingerprint(profile); value != "2789a563ab418a25ad4ec677abfafbe120560fa0d55f0db6ad92dc7984e3f2ae" {
		t.Fatalf("fingerprint mismatch: %s", value)
	}
	profile.EncryptedKey = ""
	if value := connectionFingerprint(profile); value == "2789a563ab418a25ad4ec677abfafbe120560fa0d55f0db6ad92dc7984e3f2ae" {
		t.Fatal("missing key fingerprint must change")
	}
}
