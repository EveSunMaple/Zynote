package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

func fetcherFor(server *httptest.Server) Fetcher {
	return func(target string, headers map[string]string, ctx context.Context) (*http.Response, error) {
		parsed, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		local := server.URL + parsed.Path
		if parsed.RawQuery != "" {
			local += "?" + parsed.RawQuery
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, local, nil)
		if err != nil {
			return nil, err
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		return http.DefaultClient.Do(request)
	}
}

func TestPlatformInput(t *testing.T) {
	cases := []struct {
		input    string
		platform string
		url      string
		id       string
	}{
		{"BV1xx411c7mD", "bilibili", "BV1xx411c7mD", ""},
		{"https://www.bilibili.com/video/BV1xx411c7mD/?p=2", "bilibili", "https://www.bilibili.com/video/BV1xx411c7mD/?p=2", ""},
		{"https://youtu.be/dQw4w9WgXcQ", "youtube", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "youtube", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/playlist?list=PL1234567890", "youtube", "https://www.youtube.com/playlist?list=PL1234567890", ""},
		{"https://vimeo.com/123456/abcDEF", "vimeo", "https://vimeo.com/123456/abcDEF", "123456"},
		{"https://dai.ly/x8abc12", "dailymotion", "https://www.dailymotion.com/video/x8abc12", "x8abc12"},
	}
	for _, testCase := range cases {
		selection, err := Input(testCase.input)
		if err != nil || selection.Platform != testCase.platform || selection.URL != testCase.url ||
			selection.ID != testCase.id {
			t.Fatalf("input mismatch %s: %+v %v", testCase.input, selection, err)
		}
	}
	for _, value := range []string{
		"https://example.com/video", "https://vimeo.com/abc", "https://vimeo.com/123/abc?h=xyz",
		"http://youtu.be/dQw4w9WgXcQ", "not a url",
	} {
		if _, err := Input(value); err == nil {
			t.Fatalf("expected invalid input: %s", value)
		}
	}
}

func TestReadVimeo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"video":{"id":123456,"title":"示例视频","duration":10.5,"thumbs":{"100":"https://i.vimeocdn.com/small.jpg","640":"https://i.vimeocdn.com/large.jpg"}},
			"request":{"files":{"progressive":[
				{"id":"p1","url":"https://player.vimeocdn.com/1.mp4","height":720,"mime":"video/mp4","bitrate":1500},
				{"id":"p2","url":"https://player.vimeocdn.com/2.webm","height":1080,"mime":"video/webm"}
			]},"text_tracks":[{"lang":"zh-Hans","url":"/t/1.vtt"}]}
		}`))
	}))
	defer server.Close()
	source, err := Read(context.Background(), Selection{
		Platform: "vimeo", URL: "https://vimeo.com/123456", ID: "123456",
	}, false, mediaOptions(), fetcherFor(server), nil)
	if err != nil {
		t.Fatal(err)
	}
	row := source.(media.Source)
	if row.Identity != "vimeo:123456" || row.Platform != "vimeo" || row.Catalog.Title != "示例视频" ||
		len(row.Streams) != 2 || row.Streams[0].Height != 720 || row.Streams[1].Extension != "webm" ||
		row.Catalog.Heights[0] != 720 || row.Catalog.Heights[1] != 1080 || row.Thumbnail != "https://i.vimeocdn.com/large.jpg" {
		t.Fatalf("vimeo source mismatch: %+v", row)
	}
	if len(row.SubtitleTracks) != 1 || row.SubtitleTracks[0].Language != "zh-Hans" ||
		row.SubtitleTracks[0].URL != "https://player.vimeo.com/t/1.vtt" {
		t.Fatalf("vimeo subtitles mismatch: %+v", row.SubtitleTracks)
	}
}

func TestReadVimeoRejectsIdentityMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"video":{"id":999,"title":"x","duration":1},"request":{"files":{"progressive":[]}}}`))
	}))
	defer server.Close()
	if _, err := Read(context.Background(), Selection{
		Platform: "vimeo", URL: "https://vimeo.com/123456", ID: "123456",
	}, false, mediaOptions(), fetcherFor(server), nil); err == nil || !strings.Contains(err.Error(), "身份不一致") {
		t.Fatalf("identity mismatch: %v", err)
	}
}

func TestReadDailymotion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"x8abc12","title":"示例","duration":20,"description":"简介",
			"qualities":{"720":[{"type":"video/mp4","url":"https://www.dailymotion.com/cdn/720.mp4"}]},
			"posters":{"720":"https://s1.dmcdn.net/poster.jpg"},
			"subtitles":{"data":{"en":{"urls":["https://www.dailymotion.com/cdn/en.vtt"]}}}
		}`))
	}))
	defer server.Close()
	source, err := Read(context.Background(), Selection{
		Platform: "dailymotion", URL: "https://www.dailymotion.com/video/x8abc12", ID: "x8abc12",
	}, false, mediaOptions(), fetcherFor(server), nil)
	if err != nil {
		t.Fatal(err)
	}
	row := source.(media.Source)
	if row.Identity != "dailymotion:x8abc12" || len(row.Streams) != 1 || row.Streams[0].ID != "dm-720-0" ||
		row.Description != "简介" || row.Thumbnail != "https://s1.dmcdn.net/poster.jpg" ||
		len(row.SubtitleTracks) != 1 || row.SubtitleTracks[0].Language != "en" {
		t.Fatalf("dailymotion source mismatch: %+v", row)
	}
}

type fakeCaller struct {
	method string
	raw    string
}

func (caller *fakeCaller) Call(method string, params any, timeoutMS int) (json.RawMessage, error) {
	caller.method = method
	return json.RawMessage(caller.raw), nil
}

func TestReadYoutube(t *testing.T) {
	source := `{
		"platform":"youtube","sourceId":"dQw4w9WgXcQ","identity":"youtube:dQw4w9WgXcQ",
		"streams":[{"id":"137","kind":"video","urls":["https://rr1---sn.googlevideo.com/v.mp4"],"height":1080,"codec":"avc1","bandwidth":1000,"extension":"mp4"}],
		"subtitleTracks":[{"language":"en","url":"https://www.youtube.com/api/timedtext?v=x&fmt=json3","automatic":false,"format":"youtube-json3"}],
		"catalog":{"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ","title":"标题","duration":10,"heights":[1080],"formats":[]}
	}`
	caller := &fakeCaller{raw: source}
	result, err := Read(context.Background(), Selection{
		Platform: "youtube", URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", ID: "dQw4w9WgXcQ",
	}, false, mediaOptions(), nil, caller)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := result.(media.Source)
	if !ok || row.Identity != "youtube:dQw4w9WgXcQ" || len(row.Streams) != 1 || len(row.SubtitleTracks) != 1 ||
		caller.method != "youtube.video" {
		t.Fatalf("youtube source mismatch: %+v %s", row, caller.method)
	}
	options := mediaOptions()
	options.FormatIDs = "999"
	if _, err := Read(context.Background(), Selection{
		Platform: "youtube", URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", ID: "dQw4w9WgXcQ",
	}, false, options, nil, caller); err == nil || !strings.Contains(err.Error(), "格式已改变") {
		t.Fatalf("youtube format mismatch: %v", err)
	}
	caller.raw = `{"url":"https://www.youtube.com/playlist?list=PL1234567890","title":"课程","playlist":true,"entries":[{"title":"P1","url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}]}`
	catalog, err := Read(context.Background(), Selection{
		Platform: "youtube", URL: "https://www.youtube.com/playlist?list=PL1234567890", Hash: "PL1234567890",
	}, true, mediaOptions(), nil, caller)
	if err != nil || catalog.(media.Catalog).Title != "课程" || caller.method != "youtube.playlist" {
		t.Fatalf("youtube playlist mismatch: %+v %v", catalog, err)
	}
}

func TestReadRejectsPlaylistAndFormatMismatch(t *testing.T) {
	if _, err := Read(context.Background(), Selection{Platform: "vimeo"}, true, mediaOptions(), nil, nil); err == nil ||
		!strings.Contains(err.Error(), "逐条选择") {
		t.Fatalf("playlist mismatch: %v", err)
	}
	options := mediaOptions()
	options.FormatIDs = "missing"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{
			"id":"x8abc12","title":"示例","duration":20,
			"qualities":{"720":[{"type":"video/mp4","url":"https://www.dailymotion.com/cdn/720.mp4"}]}
		}`))
	}))
	defer server.Close()
	if _, err := Read(context.Background(), Selection{
		Platform: "dailymotion", URL: "https://www.dailymotion.com/video/x8abc12", ID: "x8abc12",
	}, false, options, fetcherFor(server), nil); err == nil || !strings.Contains(err.Error(), "格式已改变") {
		t.Fatalf("format mismatch: %v", err)
	}
	if _, err := Read(context.Background(), Selection{Platform: "vimeo", ID: "1"}, true, mediaOptions(), nil, nil); err == nil ||
		!strings.Contains(err.Error(), "逐条选择") {
		t.Fatalf("playlist mismatch: %v", err)
	}
}

func mediaOptions() media.NativeMediaOptions {
	options, _ := media.MediaOptions(map[string]any{})
	return options
}
