package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sha(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func plainFetch(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return http.DefaultClient.Do(request)
}

func TestTransferMediaFull(t *testing.T) {
	payload := []byte("media-payload-0123456789")
	etag := `"v1"`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("expected identity encoding, got %q", request.Header.Get("Accept-Encoding"))
		}
		writer.Header().Set("ETag", etag)
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		writer.Write(payload)
	}))
	defer server.Close()

	directory := t.TempDir()
	progress := []int64{}
	result, err := TransferMedia(TransferInput{
		Directory: directory,
		Key:       "track-1",
		Identity:  "identity-1",
		URL:       server.URL,
		Fetch:     plainFetch,
		Ctx:       context.Background(),
		Progress:  func(downloaded int64, total *int64) { progress = append(progress, downloaded) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != int64(len(payload)) || result.Hash != sha(payload) || result.ETag != etag {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Total == nil || *result.Total != int64(len(payload)) {
		t.Fatalf("total mismatch: %+v", result.Total)
	}
	raw, _ := os.ReadFile(filepath.Join(directory, "track-1.partial"))
	if string(raw) != string(payload) {
		t.Fatalf("payload mismatch")
	}
	if len(progress) == 0 || progress[len(progress)-1] != int64(len(payload)) {
		t.Fatalf("progress not reported: %v", progress)
	}
	var journal checkpointJournal
	rawJournal, err := os.ReadFile(filepath.Join(directory, "track-1.resume.json"))
	if err != nil || json.Unmarshal(rawJournal, &journal) != nil {
		t.Fatalf("journal missing: %v", err)
	}
	if journal.Size != int64(len(payload)) || journal.Hash != sha(payload) || journal.ETag != etag {
		t.Fatalf("journal mismatch: %+v", journal)
	}
	if _, err := os.Stat(filepath.Join(directory, "track-1.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock should be removed")
	}
}

func writeResumeState(t *testing.T, directory, key, identity string, partial []byte, etag string, total int64) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, key+".partial"), partial, 0o644); err != nil {
		t.Fatal(err)
	}
	journal := checkpointJournal{Version: 1, Identity: identity, Size: int64(len(partial)), Hash: sha(partial), ETag: etag, Total: &total}
	encoded, _ := json.Marshal(journal)
	if err := os.WriteFile(filepath.Join(directory, key+".resume.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTransferMediaResume(t *testing.T) {
	full := []byte("0123456789abcdef")
	etag := `"v1"`
	prefix := full[:6]
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Range") != "bytes=6-" || request.Header.Get("If-Range") != etag {
			t.Errorf("unexpected range headers: %q %q", request.Header.Get("Range"), request.Header.Get("If-Range"))
		}
		writer.Header().Set("ETag", etag)
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes 6-15/%d", len(full)))
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(full)-6))
		writer.WriteHeader(http.StatusPartialContent)
		writer.Write(full[6:])
	}))
	defer server.Close()

	directory := t.TempDir()
	writeResumeState(t, directory, "track-1", "identity-1", prefix, etag, int64(len(full)))
	result, err := TransferMedia(TransferInput{
		Directory: directory, Key: "track-1", Identity: "identity-1", URL: server.URL, Fetch: plainFetch, Ctx: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != int64(len(full)) || result.Hash != sha(full) {
		t.Fatalf("resume mismatch: %+v", result)
	}
	raw, _ := os.ReadFile(filepath.Join(directory, "track-1.partial"))
	if string(raw) != string(full) {
		t.Fatalf("resumed payload mismatch: %q", raw)
	}
}

func TestTransferMediaCompleted416(t *testing.T) {
	full := []byte("complete-content")
	etag := `"v9"`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", etag)
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(full)))
		writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer server.Close()

	directory := t.TempDir()
	writeResumeState(t, directory, "track-1", "identity-1", full, etag, int64(len(full)))
	result, err := TransferMedia(TransferInput{
		Directory: directory, Key: "track-1", Identity: "identity-1", URL: server.URL, Fetch: plainFetch, Ctx: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != int64(len(full)) || result.Hash != sha(full) {
		t.Fatalf("416 completion mismatch: %+v", result)
	}
}

func TestTransferMediaRejectsChangedETag(t *testing.T) {
	full := []byte("0123456789abcdef")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", `"changed"`)
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes 6-15/%d", len(full)))
		writer.WriteHeader(http.StatusPartialContent)
		writer.Write(full[6:])
	}))
	defer server.Close()

	directory := t.TempDir()
	writeResumeState(t, directory, "track-1", "identity-1", full[:6], `"v1"`, int64(len(full)))
	_, err := TransferMedia(TransferInput{
		Directory: directory, Key: "track-1", Identity: "identity-1", URL: server.URL, Fetch: plainFetch, Ctx: context.Background(),
	})
	if err == nil || !strings.Contains(err.Error(), "断点响应与原分段不一致") {
		t.Fatalf("changed etag should fail: %v", err)
	}
}

func TestTransferMediaMaxBytes(t *testing.T) {
	payload := make([]byte, 2048)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", `"v1"`)
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		writer.Write(payload)
	}))
	defer server.Close()

	_, err := TransferMedia(TransferInput{
		Directory: t.TempDir(), Key: "track-1", Identity: "identity-1", URL: server.URL,
		Fetch: plainFetch, Ctx: context.Background(), MaxBytes: 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "超过此任务上限") {
		t.Fatalf("max bytes should fail: %v", err)
	}
}

func TestTransferMediaCancelKeepsCheckpoint(t *testing.T) {
	payload := []byte(strings.Repeat("x", 65536))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", `"v1"`)
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		writer.Write(payload[:8192])
		writer.(http.Flusher).Flush()
		time.Sleep(200 * time.Millisecond)
		writer.Write(payload[8192:])
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	directory := t.TempDir()
	_, err := TransferMedia(TransferInput{
		Directory: directory, Key: "track-1", Identity: "identity-1", URL: server.URL,
		Fetch: plainFetch, Ctx: ctx,
		Progress: func(downloaded int64, total *int64) {
			if downloaded >= 8192 {
				cancel()
			}
		},
	})
	if err == nil {
		t.Fatal("cancelled transfer should fail")
	}
	raw, journalErr := os.ReadFile(filepath.Join(directory, "track-1.resume.json"))
	if journalErr != nil {
		t.Fatalf("checkpoint should be preserved: %v", journalErr)
	}
	var journal checkpointJournal
	if json.Unmarshal(raw, &journal) != nil || journal.Size == 0 {
		t.Fatalf("checkpoint invalid: %s", string(raw))
	}
}

func TestTransferMediaLockContention(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "track-1.lock"), []byte("held"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := TransferMedia(TransferInput{
		Directory: directory, Key: "track-1", Identity: "identity-1", URL: "http://127.0.0.1:1", Fetch: plainFetch, Ctx: context.Background(),
	})
	if err == nil || !strings.Contains(err.Error(), "下载分段锁被占用") {
		t.Fatalf("lock contention should fail: %v", err)
	}
}

func TestOwnedArtifactLifecycle(t *testing.T) {
	directory := t.TempDir()
	plan := sha([]byte("plan"))
	artifact, err := NewOwnedMediaArtifact(directory, "track-1-hls-0.media", plan)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := artifact.Prepare()
	if err != nil || reused {
		t.Fatalf("first prepare should reserve: %v %v", reused, err)
	}
	if err := os.WriteFile(artifact.Path, []byte("artifact-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	receipt, err := artifact.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Bytes != 16 || receipt.Hash != sha([]byte("artifact-content")) || receipt.Plan != plan {
		t.Fatalf("receipt mismatch: %+v", receipt)
	}
	intermediates := artifact.Intermediates()
	if len(intermediates) != 4 {
		t.Fatalf("intermediates mismatch: %v", intermediates)
	}

	again, err := NewOwnedMediaArtifact(directory, "track-1-hls-0.media", plan)
	if err != nil {
		t.Fatal(err)
	}
	reused, err = again.Prepare()
	if err != nil || !reused {
		t.Fatalf("second prepare should reuse: %v %v", reused, err)
	}
	if err := os.WriteFile(artifact.Path, []byte("changed-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := again.Finish(); err == nil || !strings.Contains(err.Error(), "媒体核验记录已改变") {
		t.Fatalf("changed artifact should fail: %v", err)
	}
}

func TestOwnedArtifactGuards(t *testing.T) {
	directory := t.TempDir()
	plan := sha([]byte("plan"))
	if _, err := NewOwnedMediaArtifact(directory, "../escape", plan); err == nil {
		t.Fatal("path traversal should be rejected")
	}
	if _, err := NewOwnedMediaArtifact(directory, "job.json", plan); err == nil {
		t.Fatal("reserved name should be rejected")
	}
	existing, _ := NewOwnedMediaArtifact(directory, "existing.media", plan)
	if err := os.WriteFile(existing.Path, []byte("preexisting"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.Prepare(); err == nil || !strings.Contains(err.Error(), "媒体输出已经存在") {
		t.Fatalf("preexisting output should be rejected: %v", err)
	}

	otherPlan := sha([]byte("other"))
	reserved, _ := NewOwnedMediaArtifact(directory, "reserved.media", plan)
	if _, err := reserved.Prepare(); err != nil {
		t.Fatal(err)
	}
	different, _ := NewOwnedMediaArtifact(directory, "reserved.media", otherPlan)
	if _, err := different.Prepare(); err == nil || !strings.Contains(err.Error(), "已有媒体恢复记录与本次设置不同") {
		t.Fatalf("plan change should be rejected: %v", err)
	}
}

func TestOwnedArtifactArchivesIncomplete(t *testing.T) {
	directory := t.TempDir()
	plan := sha([]byte("plan"))
	artifact, err := NewOwnedMediaArtifact(directory, "partial.media", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path, []byte("incomplete-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	retry, _ := NewOwnedMediaArtifact(directory, "partial.media", plan)
	reused, err := retry.Prepare()
	if err != nil || reused {
		t.Fatalf("retry prepare should re-reserve: %v %v", reused, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	archived := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".incomplete-") && strings.HasSuffix(entry.Name(), "-partial.media") {
			archived++
		}
	}
	if archived != 1 {
		t.Fatalf("expected one archived incomplete file, got %d: %v", archived, entries)
	}
	intermediates := retry.Intermediates()
	found := false
	for _, name := range intermediates {
		if strings.HasPrefix(name, ".incomplete-") {
			found = true
		}
	}
	if !found {
		t.Fatalf("archive should be listed in intermediates: %v", intermediates)
	}
}

func TestMediaTransportRedirectAndRate(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/next", http.StatusFound)
	}))
	defer redirect.Close()
	options, _ := MediaOptions(map[string]any{})
	transport, err := NewMediaTransport(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Fetch(redirect.URL, nil, context.Background()); err == nil || !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("redirect should be rejected: %v", err)
	}

	payload := make([]byte, 32*1024)
	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		writer.Write(payload)
	}))
	defer slow.Close()
	limited, _ := MediaOptions(map[string]any{"rate_kbps": 64})
	limitedTransport, err := NewMediaTransport(limited)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response, err := limitedTransport.Fetch(slow.URL, nil, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	elapsed := time.Since(started)
	if err != nil || len(raw) != len(payload) {
		t.Fatalf("rate-limited read failed: %v %d", err, len(raw))
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("rate limiting too fast: %v", elapsed)
	}

	badProxy, _ := MediaOptions(map[string]any{"proxy": "http://127.0.0.1:1"})
	if _, err := NewMediaTransport(badProxy); err != nil {
		t.Fatalf("valid proxy form should construct: %v", err)
	}
	invalid := NativeMediaOptions{}
	invalid.Proxy = "ftp://proxy"
	if _, err := NewMediaTransport(invalid); err == nil {
		t.Fatal("unsupported proxy scheme should fail")
	}
}
