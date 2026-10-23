package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaFilename(t *testing.T) {
	options, _ := MediaOptions(map[string]any{})
	name, err := MediaFilename(options, "很长的中文标题很长的中文标题", "BV1xx411c7mD-P1", "mp4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, " [BV1xx411c7mD-P1].mp4") || len([]byte(name)) > 140 {
		t.Fatalf("filename mismatch: %q", name)
	}
	if !strings.Contains(name, "很长的中文标题") {
		t.Fatalf("title should be retained: %q", name)
	}

	restricted, _ := MediaOptions(map[string]any{"restrict_filenames": true})
	restrictedName, err := MediaFilename(restricted, "中文标题", "id-1", "mp4")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(restrictedName, "中文") || !strings.HasSuffix(restrictedName, ".mp4") {
		t.Fatalf("restricted filename mismatch: %q", restrictedName)
	}

	reserved, _ := MediaOptions(map[string]any{"filename_template": "%(title)s.%(ext)s"})
	reservedName, err := MediaFilename(reserved, "CON", "id", "mp4")
	if err != nil || !strings.HasPrefix(reservedName, "_CON") {
		t.Fatalf("reserved name mismatch: %q %v", reservedName, err)
	}

	if _, err := MediaFilename(options, "x", "id", "MP4"); err == nil {
		t.Fatal("uppercase extension should fail")
	}
	unknown, _ := MediaOptions(map[string]any{"filename_template": "%(foo)s.%(ext)s"})
	if _, err := MediaFilename(unknown, "x", "id", "mp4"); err == nil ||
		!strings.Contains(err.Error(), "未支持的字段") {
		t.Fatalf("unknown field mismatch: %v", err)
	}
	plain, _ := MediaOptions(map[string]any{"filename_template": "plain.mp4"})
	if _, err := MediaFilename(plain, "x", "id", "mp4"); err == nil {
		t.Fatal("template without fields should fail")
	}
}

func TestPublishMediaLayout(t *testing.T) {
	directory := resolvedTemp(t)
	if err := os.WriteFile(filepath.Join(directory, "result.mp4"), []byte("media-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	options, _ := MediaOptions(map[string]any{"folder_layout": "type-date"})
	result, err := PublishMediaLayout(directory, []string{"result.mp4"}, options, "2026-10-23T01:02:03.000Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs) != 1 || result.Outputs[0] != "视频/2026-10/result.mp4" {
		t.Fatalf("layout output mismatch: %+v", result)
	}
	if len(result.Cleanup) != 1 || result.Cleanup[0] != "result.mp4" {
		t.Fatalf("layout cleanup mismatch: %+v", result)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "视频", "2026-10", "result.mp4"))
	if err != nil || string(raw) != "media-bytes" {
		t.Fatalf("layout copy mismatch: %v %q", err, raw)
	}

	source, _ := MediaOptions(map[string]any{"folder_layout": "source"})
	plain, err := PublishMediaLayout(directory, []string{"result.mp4"}, source, "2026-10-23T00:00:00Z")
	if err != nil || len(plain.Cleanup) != 0 || plain.Outputs[0] != "result.mp4" {
		t.Fatalf("source layout mismatch: %+v %v", plain, err)
	}

	if _, err := PublishMediaLayout(directory, []string{"../escape.mp4"}, options, "2026-10-23T00:00:00Z"); err == nil {
		t.Fatal("traversal should fail")
	}

	if err := os.MkdirAll(filepath.Join(directory, "视频", "2026-10"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "视频", "2026-10", "result.mp4"), []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishMediaLayout(directory, []string{"result.mp4"}, options, "2026-10-23T00:00:00Z"); err == nil ||
		!strings.Contains(err.Error(), "目录整理成果校验失败") {
		t.Fatalf("existing target mismatch: %v", err)
	}

	audio, _ := MediaOptions(map[string]any{"folder_layout": "type", "audio_only": true})
	audioResult, err := PublishMediaLayout(directory, []string{"result.mp4"}, audio, "2026-10-23T00:00:00Z")
	if err != nil || audioResult.Outputs[0] != "音频/result.mp4" {
		t.Fatalf("audio layout mismatch: %+v %v", audioResult, err)
	}
}

func TestMediaSidecars(t *testing.T) {
	directory := resolvedTemp(t)
	tuning := map[string]any{"--write-description": true, "--write-link": true}
	files, err := MediaSidecars(directory, "简介内容", "https://www.bilibili.com/video/BV1/?p=1&x=<y>", tuning)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "description.txt" || files[1] != "source.webloc" {
		t.Fatalf("sidecar files mismatch: %+v", files)
	}
	description, _ := os.ReadFile(filepath.Join(directory, "description.txt"))
	if string(description) != "简介内容\n" {
		t.Fatalf("description mismatch: %q", description)
	}
	webloc, _ := os.ReadFile(filepath.Join(directory, "source.webloc"))
	if !strings.Contains(string(webloc), "&amp;") || !strings.Contains(string(webloc), "&lt;y&gt;") {
		t.Fatalf("webloc escape mismatch: %q", webloc)
	}

	if _, err := MediaSidecars(directory, "简介内容", "https://www.bilibili.com/video/BV1/?p=1&x=<y>", tuning); err != nil {
		t.Fatalf("idempotent sidecars failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "description.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MediaSidecars(directory, "简介内容", "https://www.bilibili.com/video/BV1/?p=1&x=<y>", tuning); err == nil ||
		!strings.Contains(err.Error(), "附属文件与当前来源不一致") {
		t.Fatalf("changed sidecar mismatch: %v", err)
	}

	emptyDirectory := resolvedTemp(t)
	empty, err := MediaSidecars(emptyDirectory, "", "https://example.com", map[string]any{"--write-description": true})
	if err != nil || empty[0] != "description.txt" {
		t.Fatalf("empty description mismatch: %+v %v", empty, err)
	}
	raw, _ := os.ReadFile(filepath.Join(emptyDirectory, "description.txt"))
	if string(raw) != "此视频没有提供简介。\n" {
		t.Fatalf("empty description text mismatch: %q", raw)
	}
}
