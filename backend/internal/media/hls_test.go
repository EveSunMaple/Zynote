package media

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func rangePayload(size int) []byte {
	payload := make([]byte, size)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	return payload
}

func rangeServer(t *testing.T, payload []byte, etag string, acceptRanges bool) *httptest.Server {
	t.Helper()
	rangePattern := regexp.MustCompile(`^bytes=(\d+)-(\d+)$`)
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", etag)
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		if acceptRanges {
			writer.Header().Set("Accept-Ranges", "bytes")
		}
		if request.Method == http.MethodHead {
			return
		}
		rangeHeader := request.Header.Get("Range")
		if rangeHeader == "" {
			writer.Write(payload)
			return
		}
		match := rangePattern.FindStringSubmatch(rangeHeader)
		if match == nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		start, _ := strconv.Atoi(match[1])
		end, _ := strconv.Atoi(match[2])
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		writer.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		writer.WriteHeader(http.StatusPartialContent)
		writer.Write(payload[start : end+1])
	}))
}

func headFetch(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return http.DefaultClient.Do(request)
}

func TestParallelRanges(t *testing.T) {
	payload := rangePayload(4 * 16384)
	etag := `"par-1"`
	server := rangeServer(t, payload, etag, true)
	defer server.Close()

	directory := t.TempDir()
	result, err := TransferMediaRanges(ParallelInput{
		TransferInput: TransferInput{
			Directory: directory, Key: "track-1", Identity: "identity-1", URL: server.URL,
			Fetch: plainFetch, Ctx: context.Background(),
		},
		Concurrency: 2,
		ChunkBytes:  16384,
		Head:        headFetch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != int64(len(payload)) || result.Hash != sha(payload) || result.Warning != "" {
		t.Fatalf("parallel result mismatch: %+v", result)
	}
	if len(result.Intermediates) != 12 {
		t.Fatalf("intermediates mismatch: %d", len(result.Intermediates))
	}
	raw, err := os.ReadFile(filepath.Join(directory, "track-1.partial"))
	if err != nil || !bytes.Equal(raw, payload) {
		t.Fatalf("merged payload mismatch: %v", err)
	}
	journal, err := os.ReadFile(filepath.Join(directory, "track-1.resume.json"))
	if err != nil || !strings.Contains(string(journal), sha(payload)) {
		t.Fatalf("journal mismatch: %s %v", string(journal), err)
	}
}

func TestParallelFallbackWithoutRanges(t *testing.T) {
	payload := rangePayload(32768)
	server := rangeServer(t, payload, `"par-2"`, false)
	defer server.Close()

	result, err := TransferMediaRanges(ParallelInput{
		TransferInput: TransferInput{
			Directory: t.TempDir(), Key: "track-1", Identity: "identity-1", URL: server.URL,
			Fetch: plainFetch, Ctx: context.Background(),
		},
		Concurrency: 2,
		ChunkBytes:  16384,
		Head:        headFetch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash != sha(payload) || result.Warning != "服务器未提供可核验的分段范围，已使用完整串行下载" {
		t.Fatalf("fallback mismatch: %+v", result)
	}
}

func TestParallelFallbackServerIgnoresRange(t *testing.T) {
	payload := rangePayload(32768)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", `"par-3"`)
		writer.Header().Set("Accept-Ranges", "bytes")
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		if request.Method == http.MethodHead {
			return
		}
		writer.Write(payload)
	}))
	defer server.Close()

	result, err := TransferMediaRanges(ParallelInput{
		TransferInput: TransferInput{
			Directory: t.TempDir(), Key: "track-1", Identity: "identity-1", URL: server.URL,
			Fetch: plainFetch, Ctx: context.Background(),
		},
		Concurrency: 2,
		ChunkBytes:  16384,
		Head:        headFetch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash != sha(payload) || result.Warning != "服务器未执行并行范围，已切换串行下载" || len(result.Intermediates) == 0 {
		t.Fatalf("ignored-range fallback mismatch: %+v", result)
	}
}

func TestParseHlsVod(t *testing.T) {
	manifest, err := ParseHls(`#EXTM3U
#EXT-X-MEDIA-SEQUENCE:5
#EXTINF:2.5,
seg0.ts
#EXTINF:3,
seg1.ts
#EXT-X-ENDLIST
`, "https://media.example.com/path/playlist.m3u8", func(value string) (string, error) { return value, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Segments) != 2 || manifest.Segments[0].Sequence != 5 || manifest.Segments[1].Sequence != 6 {
		t.Fatalf("segments mismatch: %+v", manifest.Segments)
	}
	if manifest.Segments[0].URL != "https://media.example.com/path/seg0.ts" || manifest.Segments[1].Duration != 3 {
		t.Fatalf("segment details mismatch: %+v", manifest.Segments)
	}
	if len(manifest.Variants) != 0 || manifest.Init != "" {
		t.Fatalf("unexpected manifest fields: %+v", manifest)
	}
}

func TestParseHlsMaster(t *testing.T) {
	manifest, err := ParseHls(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",DEFAULT=YES,URI="audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud"
video.m3u8
`, "https://media.example.com/master.m3u8", func(value string) (string, error) { return value, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Variants) != 1 || manifest.Variants[0].Height != 720 || manifest.Variants[0].Bandwidth != 2000000 {
		t.Fatalf("variant mismatch: %+v", manifest.Variants)
	}
	if manifest.Variants[0].URL != "https://media.example.com/video.m3u8" || manifest.Variants[0].Audio != "aud" {
		t.Fatalf("variant url mismatch: %+v", manifest.Variants[0])
	}
	if len(manifest.Audio) != 1 || !manifest.Audio[0].Preferred || manifest.Audio[0].Group != "aud" {
		t.Fatalf("audio mismatch: %+v", manifest.Audio)
	}
}

func TestParseHlsErrors(t *testing.T) {
	check := func(value string) (string, error) { return value, nil }
	cases := []struct {
		name    string
		text    string
		message string
	}{
		{"live", "#EXTM3U\n#EXTINF:2,\nseg.ts\n", "仅支持已结束且完整的 HLS 录像，未把直播窗口当作完整视频"},
		{"byte range", "#EXTM3U\n#EXT-X-BYTERANGE:100@0\n#EXTINF:2,\nseg.ts\n#EXT-X-ENDLIST\n", "此 HLS 字节范围或缺失片段尚不支持"},
		{"bad key", "#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"k\"\n#EXTINF:2,\nseg.ts\n#EXT-X-ENDLIST\n", "此 HLS 加密格式尚不支持"},
		{"bad duration", "#EXTM3U\n#EXTINF:0,\nseg.ts\n#EXT-X-ENDLIST\n", "HLS 片段时间无效"},
		{"missing sequence", "#EXTM3U\n#EXT-X-ENDLIST\n", "仅支持已结束且完整的 HLS 录像，未把直播窗口当作完整视频"},
		{"bad header", "#EXTINF:2,\nseg.ts\n", "HLS 清单无效或过大"},
		{"bad attrs", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=1x1\n#EXTINF:2,\nseg.ts\n", "HLS 清单结构不完整"},
	}
	for _, testCase := range cases {
		if _, err := ParseHls(testCase.text, "https://example.com/a.m3u8", check); err == nil || err.Error() != testCase.message {
			t.Fatalf("%s: expected %q, got %v", testCase.name, testCase.message, err)
		}
	}
}

func TestDownloadNativeHls(t *testing.T) {
	seg0 := []byte("segment-zero-bytes")
	seg1 := []byte("segment-one-bytes!")
	playlist := "#EXTM3U\n#EXTINF:2,\nseg0.ts\n#EXTINF:2,\nseg1.ts\n#EXT-X-ENDLIST\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/playlist.m3u8":
			writer.Write([]byte(playlist))
		case "/seg0.ts":
			writer.Write(seg0)
		case "/seg1.ts":
			writer.Write(seg1)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, err := DownloadNativeHls(HlsInput{
		Directory: t.TempDir(),
		Key:       "track-1",
		Identity:  "identity-1",
		URL:       server.URL + "/playlist.m3u8",
		Headers:   map[string]string{},
		Ctx:       context.Background(),
		Fetch:     plainFetch,
		Check:     func(value string) (string, error) { return value, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tracks) != 1 || result.Tracks[0].Kind != "combined" {
		t.Fatalf("track mismatch: %+v", result.Tracks)
	}
	raw, err := os.ReadFile(result.Tracks[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, append(append([]byte{}, seg0...), seg1...)) {
		t.Fatalf("merged media mismatch: %q", raw)
	}
	if len(result.Intermediates) < 7 {
		t.Fatalf("intermediates mismatch: %v", result.Intermediates)
	}
}

func pkcs7Pad(value []byte) []byte {
	padding := aes.BlockSize - len(value)%aes.BlockSize
	result := append([]byte{}, value...)
	for index := 0; index < padding; index++ {
		result = append(result, byte(padding))
	}
	return result
}

func TestDownloadNativeHlsEncrypted(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("abcdefghijklmnop")
	plain0 := []byte("encrypted-segment-zero")
	plain1 := []byte("encrypted-segment-one!")
	encrypt := func(value []byte) []byte {
		block, err := aes.NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		padded := pkcs7Pad(value)
		result := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(result, padded)
		return result
	}
	seg0 := encrypt(plain0)
	seg1 := encrypt(plain1)
	playlist := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\",IV=0x6162636465666768696a6b6c6d6e6f70\n#EXTINF:2,\nseg0.ts\n#EXTINF:2,\nseg1.ts\n#EXT-X-ENDLIST\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/playlist.m3u8":
			writer.Write([]byte(playlist))
		case "/key.bin":
			writer.Write(key)
		case "/seg0.ts":
			writer.Write(seg0)
		case "/seg1.ts":
			writer.Write(seg1)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, err := DownloadNativeHls(HlsInput{
		Directory: t.TempDir(),
		Key:       "track-2",
		Identity:  "identity-2",
		URL:       server.URL + "/playlist.m3u8",
		Headers:   map[string]string{},
		Ctx:       context.Background(),
		Fetch:     plainFetch,
		Check:     func(value string) (string, error) { return value, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(result.Tracks[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, append(append([]byte{}, plain0...), plain1...)) {
		t.Fatalf("decrypted media mismatch: %q", raw)
	}
}
