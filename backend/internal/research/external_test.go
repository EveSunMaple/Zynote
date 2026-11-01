package research

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const publicTestIP = "93.184.216.34"

func mapLookup(hosts map[string]string) lookupIPFunc {
	return func(_ context.Context, host string) ([]net.IPAddr, error) {
		ip, ok := hosts[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
}

// startConnectProxy 起一个本地回环 HTTP CONNECT 代理，所有隧道都转发到 target。
func startConnectProxy(t *testing.T, target string) (*url.URL, *atomic.Int32) {
	t.Helper()
	connects := &atomic.Int32{}
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			http.Error(writer, "only CONNECT", http.StatusMethodNotAllowed)
			return
		}
		connects.Add(1)
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadGateway)
			return
		}
		client, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		client.Close()
	}))
	t.Cleanup(proxy.Close)
	parsed, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed, connects
}

// newProxiedTestExternal 返回经本地代理访问 TLS 测试服务器的客户端，以及代理收到的 CONNECT 次数。
func newProxiedTestExternal(t *testing.T, handler http.HandlerFunc, hosts map[string]string) (*External, *atomic.Int32) {
	t.Helper()
	target := httptest.NewTLSServer(handler)
	t.Cleanup(target.Close)
	proxyURL, connects := startConnectProxy(t, target.Listener.Addr().String())
	tlsConfig := target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	external := newExternal(func(*http.Request) (*url.URL, error) { return proxyURL, nil }, mapLookup(hosts), tlsConfig)
	return external, connects
}

func TestExternalProxyAllowedTargetChecked(t *testing.T) {
	external, connects := newProxiedTestExternal(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}, map[string]string{"example.com": publicTestIP})
	body, err := external.get(context.Background(), "https://example.com/works")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` || connects.Load() != 1 {
		t.Fatalf("proxy request mismatch: body=%q connects=%d", body, connects.Load())
	}
}

func TestExternalProxyRejectsPrivateTarget(t *testing.T) {
	cases := map[string]map[string]string{
		"private":    {"example.com": "10.0.0.8"},
		"loopback":   {"example.com": "127.0.0.1"},
		"unresolved": {},
	}
	for name, hosts := range cases {
		t.Run(name, func(t *testing.T) {
			external, connects := newProxiedTestExternal(t, func(http.ResponseWriter, *http.Request) {}, hosts)
			if _, err := external.get(context.Background(), "https://example.com/works"); err == nil {
				t.Fatal("expected target rejection")
			}
			if connects.Load() != 0 {
				t.Fatalf("proxy should not be contacted, got %d", connects.Load())
			}
		})
	}
}

func TestExternalProxyRedirectChecked(t *testing.T) {
	external, connects := newProxiedTestExternal(t, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://internal.example.com/secret", http.StatusFound)
	}, map[string]string{"example.com": publicTestIP, "internal.example.com": "192.168.1.10"})
	_, err := external.get(context.Background(), "https://example.com/works")
	if err == nil || !strings.Contains(err.Error(), "不受信任") {
		t.Fatalf("redirect to private target must fail, got %v", err)
	}
	if connects.Load() != 1 {
		t.Fatalf("only the first hop may reach the proxy, got %d", connects.Load())
	}
}

func TestExternalDirectRejectsPrivateTarget(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("private target must not be reached")
	}))
	defer target.Close()
	tlsConfig := target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	_, port, _ := net.SplitHostPort(target.Listener.Addr().String())
	external := newExternal(func(*http.Request) (*url.URL, error) { return nil, nil },
		mapLookup(map[string]string{"example.com": "127.0.0.1"}), tlsConfig)
	_, err := external.get(context.Background(), "https://example.com:"+port+"/works")
	if err == nil || !strings.Contains(err.Error(), "不受信任") {
		t.Fatalf("direct loopback target must fail, got %v", err)
	}
}

// statusByHost 返回按主机名给出固定响应的 mock 客户端；响应体为 "429"/"500" 时返回对应状态码。
func statusByHost(responses map[string]string) *External {
	return &External{Client: &http.Client{Transport: externalRoundTrip(func(request *http.Request) (*http.Response, error) {
		body := responses[request.URL.Hostname()]
		status := map[string]int{"429": http.StatusTooManyRequests, "500": http.StatusInternalServerError}[body]
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}}
}

func TestExternalSearchSkipsFailedChannels(t *testing.T) {
	external := statusByHost(map[string]string{
		"api.crossref.org":        `{"status":"ok","message":{"items":[{"DOI":"10.1000/xyz123","title":["Taylor remainder"]}]}}`,
		"api.semanticscholar.org": "429",
		"export.arxiv.org":        "500",
	})
	rows, skips, err := external.Search(context.Background(), []string{"crossref", "semantic", "arxiv"}, "Taylor remainder", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Provider != "crossref" || len(skips) != 2 {
		t.Fatalf("partial search mismatch: rows=%+v skips=%+v", rows, skips)
	}
	if skips[0].Message != "Semantic Scholar 限流，已跳过" || skips[1].Channel != "arxiv" {
		t.Fatalf("skip messages mismatch: %+v", skips)
	}
	events := externalSkipEvents(skips)
	if len(events) != 2 || events[0]["kind"] != "external-skip" {
		t.Fatalf("skip events mismatch: %+v", events)
	}
	if err := external.Verify(rows); err != nil {
		t.Fatal(err)
	}
}

func TestExternalSearchFailsWhenAllChannelsFail(t *testing.T) {
	external := statusByHost(map[string]string{"api.crossref.org": "429", "api.semanticscholar.org": "500"})
	rows, skips, err := external.Search(context.Background(), []string{"crossref", "semantic"}, "任意中文问题", nil)
	if err == nil || rows != nil || len(skips) != 2 {
		t.Fatalf("all-failed search must error: rows=%+v skips=%+v err=%v", rows, skips, err)
	}
	if !errors.Is(err, errExternalRateLimited) || !strings.Contains(err.Error(), "所选外部渠道均检索失败") {
		t.Fatalf("aggregate error mismatch: %v", err)
	}
}
