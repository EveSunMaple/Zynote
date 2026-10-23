package bilibili

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

func TestScopedCatalogReaderUsesFetcher(t *testing.T) {
	seen := []string{}
	nav := `{"code":0,"data":{"wbi_img":{"img_url":"https://i0.hdslb.com/bfs/wbi/` +
		strings.Repeat("a", 32) + `.png","sub_url":"https://i0.hdslb.com/bfs/wbi/` + strings.Repeat("b", 32) + `.png"}}}`
	fetcher := func(target string, headers map[string]string, ctx context.Context) (*http.Response, error) {
		seen = append(seen, target)
		body := ""
		switch {
		case strings.Contains(target, "/x/web-interface/nav"):
			body = nav
		case strings.Contains(target, "/x/web-interface/view"):
			body = `{"code":0,"data":{"bvid":"BV1xx411c7mD","title":"标题","desc":"简介","pic":"https://i0.hdslb.com/bfs/archive/cover.jpg","owner":{"mid":1},"pages":[{"page":1,"cid":9,"duration":10,"part":"P1"}]}}`
		case strings.Contains(target, "/x/player/wbi/playurl"):
			if !strings.Contains(target, "w_rid=") || !strings.Contains(target, "wts=") {
				t.Errorf("playurl request is not WBI signed: %s", target)
			}
			body = `{"code":0,"data":{"dash":{"video":[{"id":80,"codecid":7,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/video.mp4","backupUrl":["https://xy106x41x209x206xy.mcdn.bilivideo.cn:8082/v1/resource/video.m4s","https://upos-sz-mirrorcos.bilivideo.com/backup.mp4"],"height":1080,"codecs":"avc1","bandwidth":1000,"mimeType":"video/mp4"}],"audio":[{"id":30280,"codecid":0,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/audio.m4a","codecs":"mp4a","mimeType":"audio/mp4"}]}}}`
		case strings.Contains(target, "/x/player/v2"):
			body = `{"code":0,"data":{"subtitle":{"subtitles":[]},"view_points":[]}}`
		default:
			t.Errorf("unexpected scoped request: %s", target)
			body = `{"code":0,"data":{}}`
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	reader := NewCatalogReader(NewScopedResources(ScopedFetcher(fetcher)))
	result, err := reader.Read("BV1xx411c7mD", false, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	source, ok := result.(media.Source)
	if !ok || source.Identity != "BV1xx411c7mD:9:1" || len(source.Streams) != 2 ||
		source.Catalog.URL != "https://www.bilibili.com/video/BV1xx411c7mD/?p=1" || source.Thumbnail == "" {
		t.Fatalf("scoped source mismatch: %+v", source)
	}
	if len(source.Streams[0].URLs) != 2 {
		t.Fatalf("untrusted backup CDN should be filtered: %+v", source.Streams[0].URLs)
	}
	if len(seen) < 4 {
		t.Fatalf("scoped fetcher not used: %+v", seen)
	}
}
