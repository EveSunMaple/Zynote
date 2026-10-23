package bilibili

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func testResources(t *testing.T, serverURL string, crypt Crypt) *Resources {
	t.Helper()
	root := filepath.Join(t.TempDir(), "MediaDownloads")
	resources := NewResources(root, crypt)
	resources.baseAPI = serverURL
	resources.basePass = serverURL
	return resources
}

func markerCrypt(data []byte, decrypt bool) ([]byte, error) {
	if decrypt {
		if len(data) == 0 || data[0] != 0x3 {
			return nil, errMarker
		}
		return append([]byte{}, data[1:]...), nil
	}
	return append([]byte{0x3}, data...), nil
}

var errMarker = &markerError{}

type markerError struct{}

func (e *markerError) Error() string { return "密文无效" }

func TestWBISign(t *testing.T) {
	var navCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/x/web-interface/nav" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&navCalls, 1)
		writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{
			"wbi_img": map[string]any{
				"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url": "https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png",
			},
		}})
	}))
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	signed, err := resources.sign(map[string]string{"keyword": "测试!()*'", "search_type": "video", "page": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(signed["w_rid"]) != 32 || !isHex(signed["w_rid"]) || signed["wts"] == "" {
		t.Fatalf("signature fields mismatch: %+v", signed)
	}
	if signed["keyword"] != "测试" {
		t.Fatalf("signature should strip forbidden chars: %+v", signed)
	}
	if _, err := resources.sign(map[string]string{"keyword": "x"}); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&navCalls) != 1 {
		t.Fatalf("signing key should be cached, nav calls=%d", navCalls)
	}
}

func writeJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write(encoded)
}

func TestReadCodeHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/x/web-interface/nav":
			writeJSON(t, writer, map[string]any{"code": -101, "data": map[string]any{"isLogin": true}})
		case "/expired":
			writeJSON(t, writer, map[string]any{"code": -101})
		case "/busy":
			writeJSON(t, writer, map[string]any{"code": 123})
		case "/broken":
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	status, err := resources.Status()
	if err != nil || status["loggedIn"] != false {
		t.Fatalf("nav -101 should be logged out: %+v %v", status, err)
	}
	if _, err := resources.read("/expired", nil, false); err == nil || !strings.Contains(err.Error(), "B 站登录已失效，请重新扫码") {
		t.Fatalf("expired error mismatch: %v", err)
	}
	if _, err := resources.read("/busy", nil, false); err == nil || !strings.Contains(err.Error(), "B 站读取失败（123）") {
		t.Fatalf("busy error mismatch: %v", err)
	}
	if _, err := resources.read("/broken", nil, false); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("http error mismatch: %v", err)
	}
}

func TestBrowsePopular(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{
			"list": []any{
				map[string]any{"bvid": "BV1xx411c7mD", "title": "<em>标题</em>&amp;", "owner": map[string]any{"name": "作者"}, "duration": 125},
				map[string]any{"bvid": "bad", "title": "无效"},
			},
		}})
	}))
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	result, err := resources.Browse("popular", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	videos := result["videos"].([]map[string]any)
	if len(videos) != 1 {
		t.Fatalf("video filter mismatch: %+v", videos)
	}
	video := videos[0]
	if video["title"] != "标题&" || video["author"] != "作者" || video["duration"] != "125" ||
		video["url"] != "https://www.bilibili.com/video/BV1xx411c7mD/" {
		t.Fatalf("video fields mismatch: %+v", video)
	}
	if result["hasMore"] != false {
		t.Fatalf("popular should not have more: %+v", result)
	}
	if _, err := resources.Browse("search", "  ", 1); err == nil || !strings.Contains(err.Error(), "请输入搜索关键词") {
		t.Fatalf("empty search mismatch: %v", err)
	}
}

func TestQRFlow(t *testing.T) {
	var pollCode atomic.Int64
	pollCode.Store(86101)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{
				"qrcode_key": "qr-key", "url": "https://passport.bilibili.com/h5/login?x=1",
			}})
		case "/x/passport-login/web/qrcode/poll":
			writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{"code": pollCode.Load()}})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	value, err := resources.Begin()
	if err != nil || value != "https://passport.bilibili.com/h5/login?x=1" {
		t.Fatalf("begin mismatch: %q %v", value, err)
	}
	image, err := QRImage(value)
	if err != nil || !strings.HasPrefix(image, "data:image/png;base64,") || len(image) < 200 {
		t.Fatalf("qr image mismatch: %v %d", err, len(image))
	}
	poll, err := resources.Poll()
	if err != nil || poll["status"] != "waiting" {
		t.Fatalf("waiting mismatch: %+v %v", poll, err)
	}
	pollCode.Store(86090)
	poll, _ = resources.Poll()
	if poll["status"] != "scanning" {
		t.Fatalf("scanning mismatch: %+v", poll)
	}
	pollCode.Store(86038)
	poll, _ = resources.Poll()
	if poll["status"] != "expired" {
		t.Fatalf("expired mismatch: %+v", poll)
	}
	poll, _ = resources.Poll()
	if poll["status"] != "expired" {
		t.Fatalf("after expiry poll mismatch: %+v", poll)
	}
	if _, err := QRImage("https://evil.example.com/x"); err == nil {
		t.Fatalf("foreign qr url should fail")
	}
}

func TestFoldersAndVideos(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/x/web-interface/nav":
			writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{
				"isLogin": true, "uname": "测试用户", "mid": 123,
			}})
		case "/x/v3/fav/folder/created/list-all":
			writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{
				"list": []any{map[string]any{"id": 5, "title": "收藏", "media_count": 2}},
			}})
		case "/x/v3/fav/resource/list":
			writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{
				"medias": []any{
					map[string]any{"bvid": "BV1xx411c7mD", "title": "视频", "duration": 60, "type": 2, "id": 1},
					map[string]any{"bvid": "BV1xx411c7mD", "title": "已失效视频", "type": 2, "id": 2},
					map[string]any{"bvid": "", "title": "音频", "type": 12, "id": 3},
				},
				"has_more": false,
			}})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	folders, err := resources.Folders()
	if err != nil || len(folders) != 1 || folders[0]["id"] != int64(5) {
		t.Fatalf("folders mismatch: %+v %v", folders, err)
	}
	if _, err := resources.Videos(6, 1); err == nil || !strings.Contains(err.Error(), "收藏夹不属于当前账号") {
		t.Fatalf("foreign folder mismatch: %v", err)
	}
	result, err := resources.Videos(5, 1)
	if err != nil {
		t.Fatal(err)
	}
	videos := result["videos"].([]map[string]any)
	if len(videos) != 3 {
		t.Fatalf("videos count mismatch: %+v", videos)
	}
	if videos[0]["available"] != true || videos[1]["available"] != false || videos[2]["available"] != false {
		t.Fatalf("availability mismatch: %+v", videos)
	}
	if result["hasMore"] != false || result["page"] != 1 {
		t.Fatalf("pagination mismatch: %+v", result)
	}
}

func TestCookiePersistence(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&requests, 1)
		if atomic.LoadInt32(&requests) == 1 {
			http.SetCookie(writer, &http.Cookie{Name: "SESSDATA", Value: "abc", Path: "/"})
		} else if !strings.Contains(request.Header.Get("Cookie"), "SESSDATA=abc") {
			t.Errorf("persisted cookie missing: %q", request.Header.Get("Cookie"))
		}
		writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{"isLogin": false}})
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "MediaDownloads")
	first := NewResources(root, markerCrypt)
	first.baseAPI = server.URL
	first.basePass = server.URL
	if _, err := first.Status(); err != nil {
		t.Fatal(err)
	}
	second := NewResources(root, markerCrypt)
	second.baseAPI = server.URL
	second.basePass = server.URL
	if _, err := second.Status(); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&requests) != 2 {
		t.Fatalf("request count mismatch: %d", requests)
	}
	if _, err := second.Logout(); err != nil {
		t.Fatal(err)
	}
}
