package bilibili

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func TestLateResponsesCannotRestoreLoggedOutSession(t *testing.T) {
	readers := map[string]func(*Resources) error{
		"api": func(r *Resources) error {
			_, err := r.readContext(context.Background(), "/x/player/v2", nil, false)
			return err
		},
		"discussion": func(r *Resources) error {
			_, err := r.RawDiscussion("/x/v2/reply", nil, context.Background(), 1024)
			return err
		},
		"subtitle": func(r *Resources) error {
			_, err := r.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
			return err
		},
	}
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			r := NewResources(t.TempDir(), markerCrypt)
			r.cookies.loaded = true
			r.cookies.cookies = []storedCookie{{Name: "SESSDATA", Value: "old", Domain: "bilibili.com", Path: "/", Secure: true}}
			entered, release := make(chan struct{}), make(chan struct{})
			r.fetch = func(request *http.Request) (*http.Response, error) {
				close(entered)
				<-release
				return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{"SESSDATA=late-old; Domain=.bilibili.com; Path=/; Secure"}}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{}}`)), Request: request}, nil
			}
			done := make(chan error, 1)
			go func() { done <- read(r) }()
			<-entered
			loggedOut := make(chan error, 1)
			go func() { _, err := r.Logout(); loggedOut <- err }()
			select {
			case err := <-loggedOut:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("logout waited on network")
			}
			close(release)
			if err := <-done; failureCode(t, err) != contract.CodeAuthExpired {
				t.Fatal(err)
			}
			cookies, err := r.BrowserCookies()
			if err != nil || len(cookies) != 0 {
				t.Fatal("old cookies restored", cookies, err)
			}
			if _, err := os.Stat(r.cookies.path); !os.IsNotExist(err) {
				t.Fatal("late response wrote cookie file", err)
			}
		})
	}
}

func TestNewLoginRotatesGenerationAndRejectsAnonymousLateResponse(t *testing.T) {
	r := NewResources(t.TempDir(), markerCrypt)
	r.cookies.loaded = true
	r.qr = &qrState{key: "fixture", expires: time.Now().Add(time.Minute).UnixMilli()}
	entered, release := make(chan struct{}), make(chan struct{})
	r.fetch = func(request *http.Request) (*http.Response, error) {
		headers := make(http.Header)
		body := `{"code":0,"data":{}}`
		if strings.Contains(request.URL.Path, "qrcode/poll") {
			body = `{"code":0,"data":{"code":0}}`
			headers.Add("Set-Cookie", "SESSDATA=new; Domain=.bilibili.com; Path=/; Secure")
			headers.Add("Set-Cookie", "bili_jct=new-csrf; Domain=.bilibili.com; Path=/; Secure")
		} else {
			close(entered)
			<-release
			headers.Add("Set-Cookie", "SESSDATA=anonymous-late; Domain=.bilibili.com; Path=/; Secure")
		}
		return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := r.readContext(context.Background(), "/x/player/v2", nil, false); done <- err }()
	<-entered
	before := r.SessionGeneration()
	result, generation, err := r.PollWithSession()
	if err != nil || result["status"] != "success" || generation <= before {
		t.Fatal(result, generation, err)
	}
	close(release)
	if err := <-done; failureCode(t, err) != contract.CodeAuthExpired {
		t.Fatal(err)
	}
	if header := r.cookies.header("api.bilibili.com", "/", true); !strings.Contains(header, "SESSDATA=new") || strings.Contains(header, "anonymous-late") {
		t.Fatal("new session overwritten", header)
	}
}

func TestCookieGenerationDoesNotAdvanceOnFailedSave(t *testing.T) {
	r := NewResources(t.TempDir(), func([]byte, bool) ([]byte, error) { return nil, errMarker })
	r.cookies.loaded = true
	r.cookies.cookies = []storedCookie{{Name: "SESSDATA", Value: "old", Domain: "bilibili.com", Path: "/"}}
	expected := r.SessionGeneration()
	response := &http.Response{Header: http.Header{"Set-Cookie": []string{"SESSDATA=new; Domain=.bilibili.com; Path=/"}}}
	if _, err := r.cookies.applyResponse("passport.bilibili.com", response, &expected, true); err == nil {
		t.Fatal("save failure ignored")
	}
	if r.SessionGeneration() != expected || r.cookies.header("api.bilibili.com", "/", false) != "SESSDATA=old" {
		t.Fatal("failed save advanced session")
	}
}

type blockedSessionBody struct {
	io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (body *blockedSessionBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() { close(body.entered); <-body.release })
	return body.Reader.Read(buffer)
}
func (*blockedSessionBody) Close() error { return nil }

func TestLogoutDuringBodyReadRejectsEntireOldResponse(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "subtitle", true: "discussion"}[raw], func(t *testing.T) {
			r := NewResources(t.TempDir(), markerCrypt)
			r.cookies.loaded = true
			body := &blockedSessionBody{Reader: strings.NewReader(`{"code":0,"data":[{"page":1,"part":"lecture","duration":20,"cid":7}]}`), entered: make(chan struct{}), release: make(chan struct{})}
			r.fetch = func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{"SESSDATA=old; Domain=.bilibili.com; Path=/; Secure"}}, Body: body, Request: request}, nil
			}
			done := make(chan error, 1)
			go func() {
				if raw {
					_, err := r.RawDiscussion("/x/v2/reply", nil, context.Background(), 1024)
					done <- err
				} else {
					_, err := r.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
					done <- err
				}
			}()
			<-body.entered
			if _, err := r.Logout(); err != nil {
				t.Fatal(err)
			}
			close(body.release)
			if err := <-done; failureCode(t, err) != contract.CodeAuthExpired {
				t.Fatal("old body accepted", err)
			}
			if cookies, err := r.BrowserCookies(); err != nil || len(cookies) != 0 {
				t.Fatal(cookies, err)
			}
		})
	}
}

func TestFailedLogoutKeepsGenerationAndReturnsFailure(t *testing.T) {
	r := NewResources(t.TempDir(), markerCrypt)
	r.cookies.loaded = true
	r.cookies.cookies = []storedCookie{{Name: "SESSDATA", Value: "old", Domain: "bilibili.com", Path: "/"}}
	if err := os.MkdirAll(r.cookies.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.cookies.path, "block-removal"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	before := r.SessionGeneration()
	if result, err := r.Logout(); err == nil || result != nil {
		t.Fatal("failed clear claimed logout", result, err)
	}
	if r.SessionGeneration() != before || r.cookies.header("api.bilibili.com", "/", false) != "SESSDATA=old" {
		t.Fatal("failed logout changed session")
	}
}
