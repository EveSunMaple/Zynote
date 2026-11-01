package videoppt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationSourceRejectsUntrustedLocators(t *testing.T) {
	for _, suffix := range []string{"?p=1&p=2", "?p=01", "?p=", "?p=100000", "?bad=%zz"} {
		if _, err := operationSource(VideoOperation{URL: "https://www.bilibili.com/video/BV1234567890/" + suffix}); err == nil {
			t.Fatal(suffix)
		}
	}
	for _, address := range []string{"https://www.bilibili.com:443/video/BV1234567890", "https://user@www.bilibili.com/video/BV1234567890", "http://www.bilibili.com/video/BV1234567890", "https://www.bilibili.com/video/BVbad"} {
		if _, err := operationSource(VideoOperation{URL: address}); err == nil {
			t.Fatal(address)
		}
	}
	for _, locator := range []string{"BV1234567890", "av123"} {
		source, err := operationSource(VideoOperation{URL: "https://www.bilibili.com/video/" + locator + "?p=2&share_source=test"})
		if err != nil || source.Page != 2 || source.BVID != "" || strings.Contains(source.URL, "share") {
			t.Fatal(source, err)
		}
	}
	if _, err := operationSource(VideoOperation{Path: "relative.mp4", SourceHash: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("relative path trusted")
	}
}

func TestCatalogSourceRequiresMatchingIdentityAndExcludesAcquisitionData(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	task, err := service.CreateOperationTask("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "lecture", VideoOperation{Version: 1, Stage: "source", URL: "https://www.bilibili.com/video/BV1234567890/?p=2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		bvid     string
		cid      int64
		page     int
		identity string
	}{
		{"BV0987654321", 1, 2, "BV0987654321:1:2"}, {"BV1234567890", 1, 1, "BV1234567890:1:1"}, {"BV1234567890", 0, 2, "BV1234567890:0:2"}, {"BV1234567890", 1, 2, "wrong"},
	} {
		if service.ResolveBiliSource(task.ID, input.bvid, input.cid, input.page, input.identity) == nil {
			t.Fatal(input)
		}
	}
	if err := service.ResolveBiliSource(task.ID, "BV1234567890", 7, 2, "BV1234567890:7:2"); err != nil {
		t.Fatal(err)
	}
	source, err := service.MediaSource(task.ID)
	if err != nil || source.Status != "resolved" || source.SHA256 != "" || source.CID != 7 {
		t.Fatal(source, err)
	}
	body, err := os.ReadFile(filepath.Join(task.Dir, "media-source.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(body, &fields) != nil || len(fields) != 7 {
		t.Fatal(string(body))
	}
}

func TestLocalSourceRequiresIndependentReadyProof(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("video fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := VideoDigest(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	task, err := service.CreateOperationTask("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "lecture", VideoOperation{Version: 1, Stage: "source", Path: sourcePath, SourceHash: digest})
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.MediaSource(task.ID)
	if err != nil || source.Status != "unresolved" {
		t.Fatal(source, err)
	}
	if err := CopyVideoReady(context.Background(), sourcePath, task.Path); err != nil {
		t.Fatal(err)
	}
	source, err = service.MediaSource(task.ID)
	if err != nil || source.Status != "resolved" || source.SHA256 != digest {
		t.Fatal(source, err)
	}
	if err := os.WriteFile(task.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.MediaSource(task.ID); err == nil {
		t.Fatal("changed copy trusted")
	}
	source.SHA256 = strings.Repeat("a", 64)
	if err := saveTaskJSON(filepath.Join(task.Dir, "media-source.json"), source); err != nil {
		t.Fatal(err)
	}
	if _, err := service.MediaSource(task.ID); err == nil {
		t.Fatal("stored hash replaced bound identity")
	}
}
