package bilibili

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func failureCode(t *testing.T, err error) string {
	t.Helper()
	application, ok := err.(*contract.AppError)
	if !ok {
		t.Fatalf("not direct wire-safe application error: %T %v", err, err)
	}
	return application.Code
}

func TestBiliFailureClassificationAcrossReaders(t *testing.T) {
	for _, input := range []struct {
		name     string
		status   int
		body     string
		expected string
	}{
		{"expired", 200, `{"code":-101}`, contract.CodeAuthExpired},
		{"risk-limit", 200, `{"code":-412}`, contract.CodeRateLimited},
		{"api-limit", 200, `{"code":-509}`, contract.CodeRateLimited},
		{"http-limit", 429, ``, contract.CodeRateLimited},
		{"http-auth", 401, ``, contract.CodeAuthExpired},
		{"http-timeout", 504, ``, contract.CodeTimeout},
		{"missing-code", 200, `{"data":{}}`, contract.CodeUnavailable},
	} {
		t.Run(input.name, func(t *testing.T) {
			resources := NewScopedResources(func(string, map[string]string, context.Context) (*http.Response, error) {
				return &http.Response{StatusCode: input.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(input.body))}, nil
			})
			_, err := FetchVideoSubtitles(context.Background(), resources.HTTPClient(), "https://www.bilibili.com/video/BV1234567890/")
			if code := failureCode(t, err); code != input.expected {
				t.Fatal(code, input.expected)
			}
			response := &http.Response{StatusCode: input.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(input.body))}
			_, err = resources.decodeBiliResponse("/x/player/v2", response)
			if code := failureCode(t, err); code != input.expected {
				t.Fatal(code, input.expected)
			}
		})
	}
}

func TestBiliTransportFailuresAreSanitizedAndPreserveTypedCooldown(t *testing.T) {
	for _, input := range []struct {
		err  error
		code string
	}{
		{context.Canceled, contract.CodeCancelled}, {context.DeadlineExceeded, contract.CodeTimeout},
		{&url.Error{Op: "GET", URL: "https://fixture.invalid/signed?secret=do-not-expose", Err: contract.NewError(contract.CodeRateLimited, "controlled cooldown")}, contract.CodeRateLimited},
		{errors.New("secret signed URL"), contract.CodeUnavailable},
	} {
		resources := NewScopedResources(func(string, map[string]string, context.Context) (*http.Response, error) { return nil, input.err })
		_, err := resources.readContext(context.Background(), "/x/player/v2", nil, false)
		if failureCode(t, err) != input.code || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
		_, err = biliGet(context.Background(), resources.HTTPClient(), "https://api.bilibili.com/x/player/v2")
		if failureCode(t, err) != input.code || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
}

func TestRateLimitCooldownIsBoundedSharedAndExplicit(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	calls := 0
	resources := NewScopedResources(func(string, map[string]string, context.Context) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"999999999999999999"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	resources.now = func() time.Time { return now }
	_, err := resources.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
	if failureCode(t, err) != contract.CodeRateLimited || calls != 1 {
		t.Fatal(err, calls)
	}
	_, err = resources.readContext(context.Background(), "/x/player/v2", nil, false)
	if failureCode(t, err) != contract.CodeRateLimited || calls != 1 {
		t.Fatal("cooldown dispatched", err, calls)
	}
	application := err.(*contract.AppError)
	if application.Data.(map[string]any)["retryAfterSeconds"] != 300 {
		t.Fatal(application.Data)
	}
	now = now.Add(maximumCooldown)
	_, err = resources.readContext(context.Background(), "/x/player/v2", nil, false)
	if failureCode(t, err) != contract.CodeRateLimited || calls != 2 {
		t.Fatal("explicit request not allowed after expiry", err, calls)
	}
}

func TestAPIRateLimitAlsoGuardsRawSubtitleRequests(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	calls := 0
	resources := NewScopedResources(func(string, map[string]string, context.Context) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":-412}`))}, nil
	})
	resources.now = func() time.Time { return now }
	_, err := resources.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
	if failureCode(t, err) != contract.CodeRateLimited {
		t.Fatal(err)
	}
	_, err = resources.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
	if failureCode(t, err) != contract.CodeRateLimited || calls != 1 {
		t.Fatal(err, calls)
	}
	now = now.Add(defaultCooldown)
	_, _ = resources.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestRetryAfterBounds(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	for _, input := range []struct {
		value    string
		expected time.Duration
	}{
		{"12", 12 * time.Second}, {"-1", defaultCooldown}, {"invalid", defaultCooldown}, {"9223372036854775807", maximumCooldown},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute}, {now.Add(time.Hour).Format(http.TimeFormat), maximumCooldown},
	} {
		if actual := cooldownDuration(input.value, now); actual != input.expected {
			t.Fatal(input.value, actual)
		}
	}
}

func TestNonRateFailuresDoNotCreateCooldownOrRewriteLoginState(t *testing.T) {
	for _, input := range []struct {
		body      string
		transport error
		expected  string
	}{
		{`{"code":-101}`, nil, contract.CodeAuthExpired},
		{"", context.DeadlineExceeded, contract.CodeTimeout},
		{"", context.Canceled, contract.CodeCancelled},
	} {
		calls := 0
		resources := NewResources(t.TempDir(), markerCrypt)
		resources.cookies.loaded = true
		resources.cookies.cookies = []storedCookie{{Name: "SESSDATA", Value: "fixture", Domain: "bilibili.com", Path: "/", Secure: true}}
		resources.fetch = func(request *http.Request) (*http.Response, error) {
			calls++
			if input.transport != nil {
				return nil, input.transport
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(input.body)), Request: request}, nil
		}
		for attempt := 0; attempt < 2; attempt++ {
			_, err := resources.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
			if failureCode(t, err) != input.expected {
				t.Fatal(err)
			}
		}
		if calls != 2 || resources.cooldownUntil.IsZero() == false || len(resources.cookies.cookies) != 1 || resources.cookies.cookies[0].Value != "fixture" {
			t.Fatal("non-rate side effect", calls)
		}
	}
}

func TestTerminalTrackFailuresDoNotDispatchAlternativeOrLoseCode(t *testing.T) {
	for _, code := range []string{contract.CodeAuthExpired, contract.CodeRateLimited, contract.CodeTimeout, contract.CodeCancelled} {
		t.Run(code, func(t *testing.T) {
			tracks := 0
			resources := NewScopedResources(func(address string, _ map[string]string, _ context.Context) (*http.Response, error) {
				body := ""
				switch {
				case strings.Contains(address, "pagelist"):
					body = `{"code":0,"data":[{"page":1,"part":"lecture","duration":20,"cid":7}]}`
				case strings.Contains(address, "/x/player/v2"):
					body = `{"code":0,"data":{"subtitle":{"subtitles":[{"lan":"zh","subtitle_url":"https://aisubtitle.hdslb.com/first.json"},{"lan":"en","subtitle_url":"https://aisubtitle.hdslb.com/second.json"}]}}}`
				default:
					tracks++
					return nil, contract.NewError(code, "controlled terminal failure")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			_, err := resources.FetchSubtitles(context.Background(), "https://www.bilibili.com/video/BV1234567890/")
			if failureCode(t, err) != code || tracks != 1 {
				t.Fatal("alternative dispatched or code overwritten", err, tracks)
			}
		})
	}
}
