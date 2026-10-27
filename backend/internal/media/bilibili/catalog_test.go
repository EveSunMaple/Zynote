package bilibili

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func catalogServer(t *testing.T, view, playurl, player any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/x/web-interface/nav":
			writeJSON(t, writer, map[string]any{"code": 0, "data": map[string]any{
				"isLogin": true, "mid": 1,
				"wbi_img": map[string]any{
					"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
					"sub_url": "https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png",
				},
			}})
		case "/x/web-interface/view":
			writeJSON(t, writer, view)
		case "/x/player/wbi/playurl":
			writeJSON(t, writer, playurl)
		case "/x/player/v2":
			writeJSON(t, writer, player)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
}

func viewFixture() any {
	return map[string]any{"code": 0, "data": map[string]any{
		"bvid": "BV1xx411c7mD", "title": "总标题", "desc": "描述",
		"pic": "http://i0.hdslb.com/x.jpg",
		"pages": []any{
			map[string]any{"cid": 111, "page": 1, "part": "P1 开头", "duration": 120},
			map[string]any{"cid": 222, "page": 2, "part": "P2 结尾", "duration": 60},
		},
	}}
}

func playurlFixture() any {
	return map[string]any{"code": 0, "data": map[string]any{
		"timelength": 120000, "quality": 80,
		"dash": map[string]any{
			"video": []any{map[string]any{
				"id": 80, "base_url": "https://xy1.bilivideo.com/v.mp4",
				"backup_url": []any{"https://xy2.bilivideo.com/v.mp4"},
				"codecs":     "avc1.64001f", "height": 720, "bandwidth": 1000000,
				"mime_type": "video/mp4", "codecid": 7,
			}},
			"audio": []any{map[string]any{
				"id": 30280, "base_url": "https://xy1.bilivideo.com/a.m4a",
				"codecs": "mp4a.40.2", "bandwidth": 128000, "mime_type": "audio/mp4", "codecid": 0,
			}},
		},
	}}
}

func playerFixture() any {
	return map[string]any{"code": 0, "data": map[string]any{
		"subtitle": map[string]any{"subtitles": []any{
			map[string]any{"lan": "zh-CN", "subtitle_url": "https://aisubtitle.hdslb.com/x.json"},
			map[string]any{"lan": "danmaku", "subtitle_url": "https://aisubtitle.hdslb.com/d.json"},
		}},
		"view_points": []any{
			map[string]any{"from": 0, "to": 60, "content": "第一章"},
			map[string]any{"from": 60, "to": 999, "content": "越界"},
		},
	}}
}

func TestCatalogSingle(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	server := catalogServer(t, viewFixture(), playurlFixture(), playerFixture())
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	reader := NewCatalogReader(resources)
	output, err := reader.Read("https://www.bilibili.com/video/BV1xx411c7mD/?p=1", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := output.(Source)
	if !ok {
		t.Fatalf("expected source, got %T", output)
	}
	if source.Identity != "BV1xx411c7mD:111:1" || source.SourceID != "BV1xx411c7mD-P1" || source.Platform != "bilibili" {
		t.Fatalf("source identity mismatch: %+v", source)
	}
	if len(source.Streams) != 2 {
		t.Fatalf("stream count mismatch: %+v", source.Streams)
	}
	video := source.Streams[0]
	if video.ID != "v80-7" || video.Kind != "video" || video.Height != 720 || len(video.URLs) != 2 ||
		video.Extension != "mp4" || video.Codec != "avc1.64001f" {
		t.Fatalf("video stream mismatch: %+v", video)
	}
	audio := source.Streams[1]
	if audio.ID != "a30280-0" || audio.Kind != "audio" || audio.Extension != "m4a" {
		t.Fatalf("audio stream mismatch: %+v", audio)
	}
	if len(source.Catalog.Heights) != 1 || source.Catalog.Heights[0] != 720 {
		t.Fatalf("heights mismatch: %+v", source.Catalog.Heights)
	}
	if source.Catalog.Duration == nil || *source.Catalog.Duration != 120 {
		t.Fatalf("duration mismatch: %+v", source.Catalog.Duration)
	}
	if len(source.Catalog.Formats) != 2 || source.Catalog.Formats[0]["video_codec"] != "avc1.64001f" {
		t.Fatalf("formats mismatch: %+v", source.Catalog.Formats)
	}
	if len(source.SubtitleTracks) != 1 || source.SubtitleTracks[0].Language != "zh-CN" {
		t.Fatalf("subtitle mismatch: %+v", source.SubtitleTracks)
	}
	if source.Thumbnail != "https://i0.hdslb.com/x.jpg" {
		t.Fatalf("thumbnail mismatch: %q", source.Thumbnail)
	}
	if len(source.Chapters) != 1 || source.Chapters[0].Title != "第一章" {
		t.Fatalf("chapter mismatch: %+v", source.Chapters)
	}
	if source.DownloadHeaders["Referer"] != "https://www.bilibili.com/" {
		t.Fatalf("headers mismatch: %+v", source.DownloadHeaders)
	}
}

func TestCatalogPlaylist(t *testing.T) {
	server := catalogServer(t, viewFixture(), playurlFixture(), playerFixture())
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	output, err := NewCatalogReader(resources).Read("BV1xx411c7mD", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, ok := output.(Catalog)
	if !ok || !catalog.Playlist || len(catalog.Entries) != 2 {
		t.Fatalf("playlist mismatch: %+v", output)
	}
	if catalog.Entries[1]["title"] != "P2 结尾" || catalog.URL != "https://www.bilibili.com/video/BV1xx411c7mD/?p=1" {
		t.Fatalf("entry mismatch: %+v", catalog)
	}
}

func TestCatalogErrors(t *testing.T) {
	check := func(t *testing.T, view, playurl, player any, input string, message string) {
		t.Helper()
		server := catalogServer(t, view, playurl, player)
		defer server.Close()
		resources := testResources(t, server.URL, markerCrypt)
		if _, err := NewCatalogReader(resources).Read(input, false, nil); err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("expected %q, got %v", message, err)
		}
	}
	mismatched := map[string]any{"code": 0, "data": map[string]any{
		"bvid": "BV1yy411c7mD", "title": "x",
		"pages": []any{map[string]any{"cid": 1, "page": 1, "part": "p", "duration": 1}},
	}}
	check(t, mismatched, playurlFixture(), playerFixture(), "BV1xx411c7mD", "视频目录与所选视频不一致")

	duplicate := map[string]any{"code": 0, "data": map[string]any{
		"bvid": "BV1xx411c7mD", "title": "x",
		"pages": []any{
			map[string]any{"cid": 1, "page": 1, "part": "p", "duration": 1},
			map[string]any{"cid": 1, "page": 2, "part": "p2", "duration": 1},
		},
	}}
	check(t, duplicate, playurlFixture(), playerFixture(), "BV1xx411c7mD", "分集目录重复")

	missingPage := viewFixture()
	check(t, missingPage, playurlFixture(), playerFixture(), "https://www.bilibili.com/video/BV1xx411c7mD/?p=9", "所选分集不存在")

	badAddress := map[string]any{"code": 0, "data": map[string]any{
		"timelength": 1000,
		"dash": map[string]any{"video": []any{map[string]any{
			"id": 1, "base_url": "http://evil.example.com/v.mp4", "height": 720,
		}}},
	}}
	check(t, viewFixture(), badAddress, playerFixture(), "BV1xx411c7mD", "媒体格式缺少可信下载地址")

	empty := map[string]any{"code": 0, "data": map[string]any{"timelength": 1000}}
	check(t, viewFixture(), empty, playerFixture(), "BV1xx411c7mD", "未取得可用音视频格式")

	server := catalogServer(t, viewFixture(), playurlFixture(), playerFixture())
	defer server.Close()
	resources := testResources(t, server.URL, markerCrypt)
	if _, err := NewCatalogReader(resources).Read("BV1xx411c7mD", false, map[string]any{
		"format_ids": "v999-7",
	}); err == nil || !strings.Contains(err.Error(), "所选格式已改变") {
		t.Fatalf("format ids mismatch: %v", err)
	}
}

func TestVideoPolicies(t *testing.T) {
	if value, err := VideoURL("https://www.bilibili.com/video/BV1xx411c7mD/?p=5"); err != nil ||
		value != "https://www.bilibili.com/video/BV1xx411c7mD/?p=5" {
		t.Fatalf("video url mismatch: %q %v", value, err)
	}
	if _, err := VideoURL("https://evil.com/video/BV1xx411c7mD"); err == nil {
		t.Fatal("foreign host should fail")
	}
	if _, err := VideoURL("https://www.bilibili.com/video/BV1xx411c7mD/?p=0"); err == nil ||
		!strings.Contains(err.Error(), "分 P 编号无效") {
		t.Fatalf("page number mismatch: %v", err)
	}
	if !VideoMediaURL("https://xy1.bilivideo.com/v.mp4") || VideoMediaURL("http://xy1.bilivideo.com/v.mp4") ||
		VideoMediaURL("https://evil.com/v.mp4") || VideoMediaURL("https://xy1.bilivideo.com/v.m4a") {
		t.Fatal("video media url policy mismatch")
	}
	if value, err := MediaAddress("//xy1.bilivideo.com/v.mp4"); err != nil || value != "https://xy1.bilivideo.com/v.mp4" {
		t.Fatalf("media address mismatch: %q %v", value, err)
	}
	if _, err := MediaAddress("https://evil.com/v.mp4"); err == nil {
		t.Fatal("foreign media address should fail")
	}
	if _, _, err := MediaInput("BV1xx411c7mD"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MediaInput("https://www.bilibili.com/video/BV1xx411c7mD/?p=3"); err != nil {
		t.Fatal(err)
	}
}
