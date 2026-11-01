package videoppt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func playbackFixture(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(canonical, "VideoTasks")
	id := "9e326025-30b4-45d0-bd27-4a30275adccb"
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("managed media"))
	hash := hex.EncodeToString(digest[:])
	operation := VideoOperation{Version: 1, Stage: "source", Path: filepath.Join(canonical, "original.mp4"), SourceHash: hash}
	for name, value := range map[string]any{"operation.json": operation, "video-ready.json": map[string]any{"version": 1, "sha256": hash}} {
		body, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(directory, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "video.mp4"), []byte("managed media"), 0600); err != nil {
		t.Fatal(err)
	}
	return New(root), id, hash, directory
}
func TestPlaybackDeclarationExistingLocalAndMetadataFence(t *testing.T) {
	service, id, hash, directory := playbackFixture(t)
	before, err := service.PlaybackSource(context.Background(), id, hash)
	if err != nil || before.Verification != "declared" || before.SourceSHA256 != hash || before.Size != 13 {
		t.Fatalf("existing local: %+v %v", before, err)
	}
	// Metadata declarations intentionally do not read or verify actual media bytes.
	if err := os.WriteFile(filepath.Join(directory, "video.mp4"), []byte("changed media"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := service.PlaybackSource(context.Background(), id, hash)
	if err != nil || after.BindingRevision != before.BindingRevision {
		t.Fatalf("metadata only: %+v %v", after, err)
	}
	operationBody, _ := os.ReadFile(filepath.Join(directory, "operation.json"))
	var operation VideoOperation
	_ = json.Unmarshal(operationBody, &operation)
	source, _ := operationSource(operation)
	body, _ := json.Marshal(source)
	if err := os.WriteFile(filepath.Join(directory, "media-source.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	withOptional, err := service.PlaybackSource(context.Background(), id, hash)
	if err != nil || withOptional.BindingRevision == before.BindingRevision {
		t.Fatalf("optional presence not fenced: %+v %v", withOptional, err)
	}
	encoded, _ := json.Marshal(withOptional)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	if len(fields) != 8 || fields["path"] != nil || fields["relativePath"] != nil {
		t.Fatalf("unexpected projection: %s", encoded)
	}
	if err := os.WriteFile(filepath.Join(directory, "media-source.json"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PlaybackSource(context.Background(), id, hash); err == nil {
		t.Fatal("present empty metadata accepted")
	}
}
func TestPlaybackDeclarationRejectsChangedMissingOversizedAndCancelled(t *testing.T) {
	service, id, hash, directory := playbackFixture(t)
	_, err := service.PlaybackSource(context.Background(), id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	var application *contract.AppError
	if !errors.As(err, &application) || application.Code != contract.CodeConflict {
		t.Fatalf("changed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.PlaybackSource(ctx, id, hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "video-ready.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PlaybackSource(context.Background(), id, hash); err == nil {
		t.Fatal("missing ready accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "video-ready.json"), make([]byte, 65537), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PlaybackSource(context.Background(), id, hash); err == nil {
		t.Fatal("oversized metadata accepted")
	}
}
func TestPlaybackDeclarationRejectsSymlinkMetadata(t *testing.T) {
	service, id, hash, directory := playbackFixture(t)
	target := filepath.Join(t.TempDir(), "ready.json")
	body, _ := os.ReadFile(filepath.Join(directory, "video-ready.json"))
	if err := os.WriteFile(target, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "video-ready.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "video-ready.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := service.PlaybackSource(context.Background(), id, hash); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestPlaybackBiliRequiresResolvedMetadata(t *testing.T) {
	service, id, hash, directory := playbackFixture(t)
	operation := VideoOperation{Version: 1, Stage: "source", URL: "https://www.bilibili.com/video/BV1234567890/?p=2"}
	body, _ := json.Marshal(operation)
	if err := os.WriteFile(filepath.Join(directory, "operation.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PlaybackSource(context.Background(), id, hash); err == nil {
		t.Fatal("unresolved Bili accepted")
	}
	if err := service.ResolveBiliSource(id, "BV1234567890", 7, 2, "BV1234567890:7:2"); err != nil {
		t.Fatal(err)
	}
	declared, err := service.PlaybackSource(context.Background(), id, hash)
	if err != nil || declared.SourceIdentity != "BV1234567890:7:2" || declared.SourceKind != "bili" {
		t.Fatalf("resolved: %+v %v", declared, err)
	}
}

func TestPlaybackDeclarationRejectsBiliMetadataMismatchAndVersions(t *testing.T) {
	for _, name := range []string{"operation-version", "ready-version", "wrong-page", "wrong-bvid", "zero-cid", "negative-cid", "missing-ready", "wrong-ready-hash"} {
		t.Run(name, func(t *testing.T) {
			service, id, hash, directory := playbackFixture(t)
			operation := VideoOperation{Version: 1, Stage: "source", URL: "https://www.bilibili.com/video/BV1234567890/?p=2"}
			body, _ := json.Marshal(operation)
			_ = os.WriteFile(filepath.Join(directory, "operation.json"), body, 0600)
			if err := service.ResolveBiliSource(id, "BV1234567890", 7, 2, "BV1234567890:7:2"); err != nil {
				t.Fatal(err)
			}
			metadata, _ := os.ReadFile(filepath.Join(directory, "media-source.json"))
			var source contract.MediaSource
			_ = json.Unmarshal(metadata, &source)
			switch name {
			case "operation-version":
				operation.Version = 2
				body, _ = json.Marshal(operation)
				_ = os.WriteFile(filepath.Join(directory, "operation.json"), body, 0600)
			case "ready-version":
				_ = os.WriteFile(filepath.Join(directory, "video-ready.json"), []byte(`{"version":2,"sha256":"`+hash+`"}`), 0600)
			case "wrong-page":
				source.Page = 1
				source.URL = "https://www.bilibili.com/video/BV1234567890/?p=1"
			case "wrong-bvid":
				source.BVID = "BV0987654321"
			case "zero-cid":
				source.CID = 0
			case "negative-cid":
				source.CID = -1
			case "missing-ready":
				_ = os.Remove(filepath.Join(directory, "video-ready.json"))
			case "wrong-ready-hash":
				_ = os.WriteFile(filepath.Join(directory, "video-ready.json"), []byte(`{"version":1,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), 0600)
			}
			body, _ = json.Marshal(source)
			_ = os.WriteFile(filepath.Join(directory, "media-source.json"), body, 0600)
			if _, err := service.PlaybackSource(context.Background(), id, hash); err == nil {
				t.Fatal("inconsistent declaration accepted")
			}
		})
	}
}

func TestPlaybackWindowsCaseVariant(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path case compatibility")
	}
	service, id, hash, _ := playbackFixture(t)
	service.Root = strings.ToUpper(service.Root)
	if _, err := service.PlaybackSource(context.Background(), id, hash); err != nil {
		t.Fatal(err)
	}
}

func TestPlaybackWindowsRejectsNonLocalRootsBeforeMetadata(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows root syntax")
	}
	for _, root := range []string{`\\no-network.invalid\share`, `\\?\C:\Library`, `C:\Library:stream`} {
		_, err := New(root).PlaybackSource(context.Background(), "9e326025-30b4-45d0-bd27-4a30275adccb", strings.Repeat("a", 64))
		var application *contract.AppError
		if !errors.As(err, &application) || application.Code != contract.CodeUnavailable {
			t.Fatalf("nonlocal root: %v", err)
		}
	}
}
