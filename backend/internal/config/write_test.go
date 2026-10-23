package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSaveIdentity(t *testing.T) {
	root := t.TempDir()
	saved, err := SaveIdentity(root, json.RawMessage(`{"userName":" 小云 ","assistantName":" Zynote ","userAvatar":"","assistantAvatar":"","persona":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if saved.UserName != "小云" || saved.AssistantName != "Zynote" {
		t.Fatalf("saved identity mismatch: %+v", saved)
	}
	read, err := ReadIdentity(root)
	if err != nil || read.UserName != "小云" {
		t.Fatalf("read identity mismatch: %+v %v", read, err)
	}
	if _, err := SaveIdentity(root, json.RawMessage(`{"userName":"","assistantName":"Zynote","persona":""}`)); err == nil {
		t.Fatal("empty user name must fail")
	}
	if _, err := SaveIdentity(root, json.RawMessage(`{"userName":"a","assistantName":"b","userAvatar":"data:image/jpeg;base64,xx","persona":""}`)); err == nil {
		t.Fatal("non-png avatar must fail")
	}
}

func TestSaveAndClearAppearance(t *testing.T) {
	root := t.TempDir()
	value := `{"background":"mint","image":"","opacity":0.5,"blur":4,"scrim":0.3,"accent":"blue","fontSize":18,"lineHeight":1.8,"avatarSize":44,"avatarShape":"rounded","showNames":false,"assistantBubble":true,"bubbleRadius":20,"messageGap":30,"contentWidth":900}`
	state, err := SaveAppearance(root, json.RawMessage(value), nil)
	if err != nil || state.Global.Background != "mint" {
		t.Fatalf("global appearance mismatch: %+v %v", state, err)
	}
	courseID := "11111111-1111-4111-8111-111111111111"
	if !IsUUID(courseID) {
		t.Fatal("fixture uuid invalid")
	}
	state, err = SaveAppearance(root, json.RawMessage(value), &courseID)
	if err != nil || state.Courses[courseID].Background != "mint" || state.Global.Background != "mint" {
		t.Fatalf("course appearance mismatch: %+v %v", state, err)
	}
	if _, err := SaveAppearance(root, json.RawMessage(`{"background":"unknown"}`), nil); err == nil {
		t.Fatal("invalid appearance must fail")
	}
	state, err = ClearAppearance(root, courseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state.Courses[courseID]; exists {
		t.Fatalf("course appearance not cleared: %+v", state.Courses)
	}
	if _, err := ClearAppearance(root, "not-a-uuid"); err == nil {
		t.Fatal("invalid course id must fail")
	}
	files := map[string]string{"appearance.json": filepath.Join(root, "appearance.json")}
	if _, err := ReadAppearance(filepath.Dir(files["appearance.json"])); err != nil {
		t.Fatalf("appearance file unreadable: %v", err)
	}
}
