package media

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaCookieScope(t *testing.T) {
	valid := map[string]string{
		"Example.COM":      "example.com",
		".bilibili.com":    "bilibili.com",
		" sub.example.cn ": "sub.example.cn",
	}
	for input, expected := range valid {
		scope, err := MediaCookieScope(input)
		if err != nil || scope != expected {
			t.Fatalf("scope %q -> %q %v", input, scope, err)
		}
	}
	for _, input := range []string{"", "co.uk", "com.cn", "http://example.com", "example", "example.com/path", "a..b.com"} {
		if _, err := MediaCookieScope(input); err == nil {
			t.Fatalf("scope %q should fail", input)
		}
	}
}

func TestParseMediaCookies(t *testing.T) {
	text := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"# a comment",
		"#HttpOnly_.example.com\tTRUE\t/\tTRUE\t4102444800\tsession\tabc123",
		"example.com\tFALSE\t/api\tFALSE\t4102444800\ttoken\txyz",
		"example.com\tFALSE\t/api\tFALSE\t4102444800\ttoken\tduplicate",
		"other.com\tFALSE\t/\tFALSE\t4102444800\tforeign\tnope",
		"example.com\tFALSE\t/\tFALSE\t1000\texpired\tgone",
	}, "\n")
	parsed, err := ParseMediaCookies(text, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Count != 2 || len(parsed.Cookies) != 2 {
		t.Fatalf("count mismatch: %+v", parsed)
	}
	if !strings.Contains(parsed.Text, "#HttpOnly_.example.com") || !strings.Contains(parsed.Text, "token\txyz") ||
		strings.Contains(parsed.Text, "duplicate") || strings.Contains(parsed.Text, "foreign") {
		t.Fatalf("text mismatch: %q", parsed.Text)
	}
	if parsed.Cookies[0].Name != "session" || !parsed.Cookies[0].HTTPOnly || !parsed.Cookies[0].Secure {
		t.Fatalf("cookie flags mismatch: %+v", parsed.Cookies[0])
	}

	if _, err := ParseMediaCookies("a\tb\tc\n", "example.com"); err == nil || err.Error() != "请使用 Netscape cookies.txt 格式" {
		t.Fatalf("format error mismatch: %v", err)
	}
	if _, err := ParseMediaCookies("example.com\tMAYBE\t/\tFALSE\t4102444800\tn\tv", "example.com"); err == nil ||
		err.Error() != "Cookie 字段格式无效" {
		t.Fatalf("field error mismatch: %v", err)
	}
	if _, err := ParseMediaCookies("other.com\tFALSE\t/\tFALSE\t4102444800\tn\tv", "example.com"); err == nil ||
		err.Error() != "没有匹配该域名且未过期的 Cookie" {
		t.Fatalf("no match error mismatch: %v", err)
	}
	if _, err := ParseMediaCookies(strings.Repeat("x", 1_048_577), "example.com"); err == nil ||
		err.Error() != "Cookie 文件超过 1 MB" {
		t.Fatalf("size error mismatch: %v", err)
	}
}

func markerCrypt(data []byte, decrypt bool) ([]byte, error) {
	if decrypt {
		if len(data) == 0 || data[0] != 0x2 {
			return nil, fmt.Errorf("密文无效")
		}
		return append([]byte{}, data[1:]...), nil
	}
	return append([]byte{0x2}, data...), nil
}

func TestMediaAuthLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MediaDownloads")
	auth := NewMediaAuth(root, markerCrypt)
	cookieText := "example.com\tFALSE\t/\tFALSE\t4102444800\ttoken\txyz"
	saved, err := auth.Save(SaveProfileInput{Name: "示例", Site: "example.com", Cookies: cookieText})
	if err != nil {
		t.Fatal(err)
	}
	if saved["name"] != "示例" || saved["site"] != "example.com" || saved["count"] != 1 {
		t.Fatalf("saved profile mismatch: %+v", saved)
	}
	id := saved["id"].(string)
	if _, err := os.Stat(filepath.Join(root, "download-auth", "profiles.dpapi")); err != nil {
		t.Fatalf("vault missing: %v", err)
	}
	list, err := auth.List()
	if err != nil || len(list) != 1 || list[0]["id"] != id {
		t.Fatalf("list mismatch: %+v %v", list, err)
	}

	reloaded := NewMediaAuth(root, markerCrypt)
	list, err = reloaded.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("reload mismatch: %+v %v", list, err)
	}

	broken := NewMediaAuth(root, nil)
	if _, err := broken.List(); err == nil || !strings.Contains(err.Error(), "登录档案不能在当前系统用户下读取") {
		t.Fatalf("wrong crypt should fail: %v", err)
	}

	if _, err := auth.Save(SaveProfileInput{Name: "缺失", Site: "example.com", Cookies: cookieText, ID: "ffffffffffffffffffffffffffffffff"}); err == nil ||
		!strings.Contains(err.Error(), "所选登录档案不存在") {
		t.Fatalf("missing id mismatch: %v", err)
	}
	if _, err := auth.Save(SaveProfileInput{Name: "坏站", Site: "co.uk", Cookies: cookieText}); err == nil {
		t.Fatalf("bad site should fail")
	}
	if _, err := auth.Remove(id); err != nil {
		t.Fatal(err)
	}
	if list, err := auth.List(); err != nil || len(list) != 0 {
		t.Fatalf("remove mismatch: %+v %v", list, err)
	}
	if _, err := auth.Remove(id); err == nil || !strings.Contains(err.Error(), "所选登录档案不存在") {
		t.Fatalf("double remove mismatch: %v", err)
	}
}

func TestMediaAuthScoped(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MediaDownloads")
	auth := NewMediaAuth(root, markerCrypt)
	saved, err := auth.Save(SaveProfileInput{
		Name: "示例", Site: "example.com",
		Cookies: "example.com\tFALSE\t/\tFALSE\t4102444800\ttoken\txyz",
	})
	if err != nil {
		t.Fatal(err)
	}
	profileID := saved["id"].(string)
	options, _ := MediaOptions(map[string]any{"use_cookies": true, "cookie_profile": profileID})

	var captured map[string]string
	base := func(requestURL string, headers map[string]string, _ context.Context) (*http.Response, error) {
		captured = headers
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	}
	scoped, err := auth.Scoped("https://example.com/video", options, base)
	if err != nil {
		t.Fatal(err)
	}
	response, err := scoped("https://example.com/video", map[string]string{"Cookie": "stale=1", "Authorization": "Bearer x"}, context.Background())
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("scoped fetch failed: %v", err)
	}
	if captured["Cookie"] != "token=xyz" || captured["Authorization"] != "" {
		t.Fatalf("cookie headers mismatch: %+v", captured)
	}

	if _, err := scoped("http://example.com/video", nil, context.Background()); err != nil {
		t.Fatal(err)
	}
	if captured["Cookie"] != "" {
		t.Fatalf("http must not attach cookies: %+v", captured)
	}

	if _, err := auth.Scoped("https://other.com/video", options, base); err == nil ||
		!strings.Contains(err.Error(), "登录档案不属于当前视频站点") {
		t.Fatalf("foreign site mismatch: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {}))
	defer server.Close()
	plain, _ := MediaOptions(map[string]any{})
	if fetcher, err := auth.Scoped(server.URL, plain, base); err != nil || fetcher == nil {
		t.Fatalf("plain options should pass through: %v", err)
	}
}
