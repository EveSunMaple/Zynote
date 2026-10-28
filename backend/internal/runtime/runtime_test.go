package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, root string, value any) {
	t.Helper()
	encoded, _ := json.Marshal(value)
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyStatus(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, map[string]any{
		"schema": 1, "productVersion": "1.0.0",
		"components": []any{map[string]any{
			"id": "video", "label": "视频组件", "directory": "video", "bytes": 3,
			"files": []any{map[string]any{"path": "ffmpeg", "size": 3, "sha256": "abc"}},
		}},
	})
	status := CheckStatus(root, "1.0.0")
	if status.Validation != "legacy" || len(status.Components) != 1 || status.Components[0].Installed {
		t.Fatalf("status mismatch: %+v", status)
	}
	if err := os.MkdirAll(filepath.Join(root, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "video", "ffmpeg"), []byte("abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	status = CheckStatus(root, "1.0.0")
	if !status.Components[0].Installed {
		t.Fatalf("installed mismatch: %+v", status)
	}
}

func TestV2Status(t *testing.T) {
	root := t.TempDir()
	content := []byte("tool")
	sum := sha256.Sum256(content)
	writeManifest(t, root, map[string]any{
		"schema": 2, "productVersion": "1.0.0",
		"target": map[string]any{"platform": targetPlatform(), "arch": targetArch()},
		"components": []any{map[string]any{
			"id": "video", "label": "视频组件", "directory": "video", "bytes": 4,
			"target": map[string]any{"platform": targetPlatform(), "arch": targetArch()},
			"files": []any{map[string]any{
				"path": "ffmpeg", "size": 4, "sha256": hex.EncodeToString(sum[:]),
				"mode": map[bool]string{true: "0644", false: "0755"}[targetPlatform() == "win32"],
			}},
		}},
	})
	status := CheckStatus(root, "1.0.0")
	if status.Validation == "invalid" || status.Components[0].Validation != "missing" || !status.Components[0].Installable {
		t.Fatalf("missing mismatch: %+v", status)
	}
	if err := os.MkdirAll(filepath.Join(root, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if targetPlatform() != "win32" {
		mode = 0o755
	}
	if err := os.WriteFile(filepath.Join(root, "video", "ffmpeg"), content, mode); err != nil {
		t.Fatal(err)
	}
	status = CheckStatus(root, "1.0.0")
	if status.Components[0].Validation != "verified" || !status.Components[0].Installed {
		t.Fatalf("verified mismatch: %+v", status)
	}
}

func TestV2Install(t *testing.T) {
	root := t.TempDir()
	content := []byte("tool")
	sum := sha256.Sum256(content)
	mode := "0644"
	if targetPlatform() != "win32" {
		mode = "0755"
	}
	writeManifest(t, root, map[string]any{
		"schema": 2, "productVersion": "1.0.0",
		"target": map[string]any{"platform": targetPlatform(), "arch": targetArch()},
		"components": []any{map[string]any{
			"id": "video", "label": "视频组件", "directory": "video", "bytes": int64(len(content)),
			"target": map[string]any{"platform": targetPlatform(), "arch": targetArch()},
			"files": []any{map[string]any{
				"path": "ffmpeg", "size": len(content), "sha256": hex.EncodeToString(sum[:]), "mode": mode,
			}},
		}},
	})
	source := t.TempDir()
	descriptor, _ := json.Marshal(map[string]any{"id": "video", "productVersion": "1.0.0"})
	if err := os.WriteFile(filepath.Join(source, "component.json"), descriptor, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "payload", "ffmpeg"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, "1.0.0", "video", source); err != nil {
		t.Fatal(err)
	}
	status := CheckStatus(root, "1.0.0")
	if status.Components[0].Validation != "verified" || !status.Components[0].Installed {
		t.Fatalf("install result mismatch: %+v", status)
	}
	installed, err := os.ReadFile(filepath.Join(root, "video", "ffmpeg"))
	if err != nil || string(installed) != string(content) {
		t.Fatalf("installed content mismatch: %q (%v)", installed, err)
	}
}

func TestStatusRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, map[string]any{"schema": 3, "productVersion": "1.0.0", "components": []any{}})
	if status := CheckStatus(root, "1.0.0"); status.Validation != "invalid" {
		t.Fatalf("invalid status mismatch: %+v", status)
	}
}
