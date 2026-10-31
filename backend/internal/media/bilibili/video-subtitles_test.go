package bilibili

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rewriteTransport struct{ target string }

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme = "http"
	request.URL.Host = transport.target
	return http.DefaultTransport.RoundTrip(request)
}

func TestFetchVideoSeriesAndSubtitles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/x/player/pagelist":
			if request.URL.Query().Get("bvid") != "BV1xx411c7mD" {
				t.Errorf("bvid mismatch: %s", request.URL.RawQuery)
			}
			fmt.Fprint(writer, `{"code":0,"data":[{"page":1,"part":"01-第一讲","duration":120,"cid":11},{"page":2,"part":"02-第二讲","duration":90,"cid":22}]}`)
		case "/x/player/v2":
			fmt.Fprint(writer, `{"code":0,"data":{"subtitle":{"subtitles":[{"lan":"en","lan_doc":"English","subtitle_url":"https://aisubtitle.hdslb.com/en.json"},{"lan":"ai-zh","lan_doc":"中文（自动）","subtitle_url":"https://aisubtitle.hdslb.com/zh.json","ai_type":1}]}}}`)
		case "/zh.json":
			fmt.Fprint(writer, `{"body":[{"from":1.2,"to":2.4,"content":" 第一句 "},{"from":2.2,"to":5.5,"content":"第二句\u0000"}]}`)
		case "/en.json":
			fmt.Fprint(writer, `{"body":[{"from":0,"to":1,"content":"English"}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	t.Setenv("ZYNOTE_BILIBILI_API", server.URL)
	client := &http.Client{Transport: rewriteTransport{target: strings.TrimPrefix(server.URL, "http://")}}
	series, err := FetchVideoSeries(context.Background(), client, "https://www.bilibili.com/video/BV1xx411c7mD/?p=2")
	if err != nil || series.Title != "01" || len(series.Episodes) != 2 ||
		series.Episodes[1].URL != "https://www.bilibili.com/video/BV1xx411c7mD/?p=2" {
		t.Fatalf("series mismatch: %+v %v", series, err)
	}
	subtitle, err := FetchVideoSubtitles(context.Background(), client, "https://www.bilibili.com/video/BV1xx411c7mD/?p=2")
	if err != nil {
		t.Fatal(err)
	}
	if subtitle.Page != 2 || subtitle.Source != "中文（自动）" || subtitle.Count != 2 ||
		!strings.Contains(subtitle.SRT, "第一句") || !strings.Contains(subtitle.SRT, "00:00:01,200 --> 00:00:02,200") ||
		subtitle.Warning == "" {
		t.Fatalf("subtitle mismatch: %+v", subtitle)
	}
}

func TestOnlyExplicitEmptySubtitleCatalogIsNoSubtitle(t *testing.T) {
	for _, mode := range []string{"empty", "login", "rate-limit", "invalid-track", "missing-catalog", "null-catalog"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/x/player/pagelist":
					fmt.Fprint(w, `{"code":0,"data":[{"page":1,"part":"lecture","duration":120,"cid":11}]}`)
				case "/x/player/v2":
					switch mode {
					case "empty":
						fmt.Fprint(w, `{"code":0,"data":{"subtitle":{"subtitles":[]}}}`)
					case "login":
						fmt.Fprint(w, `{"code":-101}`)
					case "missing-catalog":
						fmt.Fprint(w, `{"code":0,"data":{}}`)
					case "null-catalog":
						fmt.Fprint(w, `{"code":0,"data":{"subtitle":{"subtitles":null}}}`)
					case "rate-limit":
						w.WriteHeader(http.StatusTooManyRequests)
					default:
						fmt.Fprint(w, `{"code":0,"data":{"subtitle":{"subtitles":[{"lan":"zh","subtitle_url":"https://aisubtitle.hdslb.com/track.json"}]}}}`)
					}
				default:
					fmt.Fprint(w, `{"body":[]}`)
				}
			}))
			defer server.Close()
			t.Setenv("ZYNOTE_BILIBILI_API", server.URL)
			client := &http.Client{Transport: rewriteTransport{target: strings.TrimPrefix(server.URL, "http://")}}
			_, err := FetchVideoSubtitles(context.Background(), client, "https://www.bilibili.com/video/BV1234567890/")
			if err == nil || IsNoSubtitle(err) != (mode == "empty") {
				t.Fatal(mode, err)
			}
		})
	}
}
