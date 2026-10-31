package bilibili

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQRSuccessPersistsAndRejectsMissingCookies(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "complete"}[include], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "generate") {
					io.WriteString(w, `{"code":0,"data":{"qrcode_key":"key","url":"https://passport.bilibili.com/h5/login"}}`)
					return
				}
				if include {
					http.SetCookie(w, &http.Cookie{Name: "SESSDATA", Value: "fixture", Path: "/"})
					http.SetCookie(w, &http.Cookie{Name: "bili_jct", Value: "csrf", Path: "/"})
				}
				io.WriteString(w, `{"code":0,"data":{"code":0}}`)
			}))
			defer server.Close()
			r := testResources(t, server.URL, markerCrypt)
			if _, err := r.Begin(); err != nil {
				t.Fatal(err)
			}
			result, err := r.Poll()
			if !include {
				if err == nil {
					t.Fatal("missing credentials accepted")
				}
				return
			}
			if err != nil || result["status"] != "success" {
				t.Fatalf("poll: %v %v", result, err)
			}
			next := newCookieStore(r.cookies.path, markerCrypt)
			if err := next.load(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(next.header("127.0.0.1", "/", false), "SESSDATA=fixture") {
				t.Fatal("restart lost cookie")
			}
		})
	}
}
func TestCookieSaveErrorDoesNotReportLogin(t *testing.T) {
	r := testResources(t, "https://passport.bilibili.com", func([]byte, bool) ([]byte, error) { return nil, errMarker })
	response := &http.Response{Header: http.Header{"Set-Cookie": []string{"SESSDATA=x; Path=/"}}}
	if err := r.cookies.setFromResponse("passport.bilibili.com", response); err == nil {
		t.Fatal("save failure ignored")
	}
	if len(r.cookies.cookies) != 0 {
		t.Fatal("failed save left successful memory state")
	}
}
func TestFailedLoadCanRecover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies")
	if err := os.WriteFile(path, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	store := newCookieStore(path, markerCrypt)
	if store.load() == nil {
		t.Fatal("invalid encryption accepted")
	}
	data, _ := markerCrypt([]byte(`[]`), false)
	os.WriteFile(path, data, 0600)
	if err := store.load(); err != nil {
		t.Fatal("failed load was cached", err)
	}
}
func TestCookieBoundaries(t *testing.T) {
	store := newCookieStore(filepath.Join(t.TempDir(), "c"), markerCrypt)
	store.cookies = []storedCookie{{Name: "host", Value: "x", Domain: "api.bilibili.com", Path: "/x", HostOnly: true}, {Name: "domain", Value: "x", Domain: "bilibili.com", Path: "/x"}, {Name: "expired", Value: "x", Domain: "bilibili.com", Path: "/", Expires: time.Now().Unix() - 1}}
	if got := store.header("sub.api.bilibili.com", "/xyz", true); got != "" {
		t.Fatalf("scope leak %s", got)
	}
	if got := store.header("sub.api.bilibili.com", "/x/view", true); got != "domain=x" {
		t.Fatalf("host-only leak %s", got)
	}
}
func TestSessionClientDoesNotLeakToCDNOrRedirect(t *testing.T) {
	r := NewResources(t.TempDir(), markerCrypt)
	r.cookies.loaded = true
	r.cookies.cookies = []storedCookie{{Name: "SESSDATA", Value: "fixture", Domain: "bilibili.com", Path: "/", Secure: true}}
	r.fetch = func(req *http.Request) (*http.Response, error) {
		has := strings.Contains(req.Header.Get("Cookie"), "SESSDATA=fixture")
		if has != (req.URL.Host == "api.bilibili.com") {
			t.Error("incorrect credential destination", req.URL.Host)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	}
	for _, target := range []string{"https://api.bilibili.com/x/player/v2", "https://i0.hdslb.com/subtitle", "https://api.bilibili.com.evil.test/"} {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", target, nil)
		response, err := r.HTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if r.HTTPClient().CheckRedirect(nil, nil) == nil {
		t.Fatal("redirects accepted")
	}
}
func TestSubtitleHostBoundary(t *testing.T) {
	if _, err := trustedSubtitleAddress("https://evilhdslb.com/x"); err == nil {
		t.Fatal("suffix confusion accepted")
	}
}

func TestAuthenticatedSubtitlePipeline(t *testing.T) {
	r := NewResources(t.TempDir(), markerCrypt)
	r.cookies.loaded = true
	r.cookies.cookies = []storedCookie{{Name: "SESSDATA", Value: "fixture", Domain: "bilibili.com", Path: "/", Secure: true}}
	r.fetch = func(req *http.Request) (*http.Response, error) {
		body := ""
		switch req.URL.Path {
		case "/x/player/pagelist":
			body = `{"code":0,"data":[{"page":1,"part":"Lecture","duration":20,"cid":7}]}`
		case "/x/player/v2":
			if req.Header.Get("Cookie") != "SESSDATA=fixture" {
				t.Fatal("subtitle API anonymous")
			}
			body = `{"code":0,"data":{"subtitle":{"subtitles":[{"lan":"zh","subtitle_url":"https://aisubtitle.hdslb.com/subtitle.json"}]}}}`
		case "/subtitle.json":
			if req.Header.Get("Cookie") != "" {
				t.Fatal("CDN received credential")
			}
			body = `{"body":[{"from":0,"to":4,"content":"fixture subtitle"}]}`
		default:
			t.Fatalf("unexpected URL %s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	}
	result, err := r.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1xx411c7mD/?p=1")
	if err != nil || !strings.Contains(result.SRT, "fixture subtitle") {
		t.Fatalf("pipeline: %v %v", result, err)
	}
}
