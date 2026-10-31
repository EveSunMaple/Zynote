package bilibili

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type sessionTransport struct{ resources *Resources }

// RoundTrip authenticates only Bilibili API requests; subtitle CDN requests
// never receive account cookies. Redirects are rejected by HTTPClient.
func (transport sessionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	resources := transport.resources
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	request.Header.Del("Cookie")
	base, _ := url.Parse(resources.baseAPI)
	if request.URL.Scheme == base.Scheme && request.URL.Host == base.Host && resources.cookies != nil {
		if err := resources.cookies.load(); err != nil {
			return nil, err
		}
		if cookie := resources.cookies.header(request.URL.Hostname(), request.URL.Path, request.URL.Scheme == "https"); cookie != "" {
			request.Header.Set("Cookie", cookie)
		}
	}
	return resources.do(request)
}

func (resources *Resources) HTTPClient() *http.Client {
	return &http.Client{Transport: sessionTransport{resources}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return contract.Unavailable("B 站请求不允许重定向")
	}}
}

// BrowserCookies stays on the trusted Go -> Electron reverse bridge; it must
// never be exposed as a renderer action or logged.
func (resources *Resources) BrowserCookies() ([]map[string]any, error) {
	if err := resources.cookies.load(); err != nil {
		return nil, err
	}
	resources.cookies.mu.Lock()
	defer resources.cookies.mu.Unlock()
	result := []map[string]any{}
	for _, cookie := range resources.cookies.cookies {
		domain := strings.TrimPrefix(cookie.Domain, ".")
		if domain != "bilibili.com" && !strings.HasSuffix(domain, ".bilibili.com") {
			continue
		}
		row := map[string]any{"name": cookie.Name, "value": cookie.Value, "domain": domain, "path": cookie.Path, "secure": cookie.Secure, "httpOnly": cookie.HTTPOnly, "hostOnly": cookie.HostOnly, "expires": cookie.Expires, "sameSite": cookie.SameSite}
		result = append(result, row)
	}
	return result, nil
}

func (resources *Resources) FetchSeries(ctx context.Context, value string) (VideoSeries, error) {
	return FetchVideoSeries(ctx, resources.HTTPClient(), value)
}
func (resources *Resources) FetchSubtitles(ctx context.Context, value string) (FetchedSubtitle, error) {
	return FetchVideoSubtitles(ctx, resources.HTTPClient(), value)
}
