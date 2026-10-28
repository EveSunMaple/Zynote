package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEveryGoContractActionIsRegistered(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contract", "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Actions []struct {
			Action  string `json:"action"`
			Backend string `json:"backend"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	inv := NewInvoker()
	for _, action := range contract.Actions {
		if action.Backend != "go" {
			continue
		}
		if _, ok := inv.handlers[action.Action]; ok {
			continue
		}
		if _, ok := inv.actions[action.Action]; ok {
			continue
		}
		t.Errorf("Go action not registered: %s", action.Action)
	}
}
