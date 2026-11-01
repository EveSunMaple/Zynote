package jobs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func TestSourceProjectionIsOwnerBoundAndCopied(t *testing.T) {
	registry := NewRegistry()
	source := contract.MediaSource{Kind: "local", Status: "unresolved", Path: filepath.Join(t.TempDir(), "video.mp4"), SHA256: strings.Repeat("a", 64)}
	if registry.SetSource("owner", source) {
		t.Fatal("idle source accepted")
	}
	_, err := registry.Run("owner", "", func(context.Context) (any, error) {
		if registry.SetSource("other", source) || !registry.SetSource("owner", source) {
			t.Fatal("owner binding")
		}
		source.Path = "mutated"
		current := registry.Current().(map[string]any)
		copy := current["source"].(contract.MediaSource)
		if copy.Path == "mutated" {
			t.Fatal("caller alias")
		}
		copy.Path = "other mutation"
		current["source"] = copy
		if registry.Current().(map[string]any)["source"].(contract.MediaSource).Path == copy.Path {
			t.Fatal("snapshot alias")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Current() != nil {
		t.Fatal("source outlived owner")
	}
}
