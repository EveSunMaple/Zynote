package media

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type MediaTransport struct {
	client    *http.Client
	interval  time.Duration
	rateBytes float64
	mu        sync.Mutex
	lastFetch time.Time
}

func NewMediaTransport(options NativeMediaOptions) (*MediaTransport, error) {
	tuning, err := MediaTuning(options.CustomArgs)
	if err != nil {
		return nil, err
	}
	seconds := 60.0
	if raw, ok := tuning["--socket-timeout"].(int); ok {
		seconds = float64(raw)
	}
	interval := time.Duration(0)
	if raw, ok := tuning["--sleep-requests"].(int); ok {
		interval = time.Duration(raw) * time.Second
	}
	dialer := &net.Dialer{Timeout: time.Duration(seconds * float64(time.Second))}
	transport := &http.Transport{
		DisableCompression: true,
		Proxy:              nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if options.ForceIPv4 {
				network = "tcp4"
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	if options.Proxy != "" {
		parsed, err := url.Parse(options.Proxy)
		if err != nil || parsed.Hostname() == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
			return nil, contract.InputInvalid("代理地址无效")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(seconds * float64(time.Second)),
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			return errors.New("媒体下载不允许重定向")
		},
	}
	rate := float64(options.RateKbps) * 1024
	return &MediaTransport{client: client, interval: interval, rateBytes: rate}, nil
}

func sleepWithCancel(d time.Duration, ctx context.Context) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return contract.NewError(contract.CodeCancelled, "下载等待已取消")
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return contract.NewError(contract.CodeCancelled, "下载等待已取消")
	}
}

type pacedBody struct {
	ctx      context.Context
	reader   io.ReadCloser
	schedule float64
	rate     float64
	closed   bool
}

func (body *pacedBody) Read(target []byte) (int, error) {
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	chunk, err := body.reader.Read(target)
	if chunk > 0 && body.rate > 0 {
		now := float64(time.Now().UnixNano()) / 1e6
		wait := body.schedule - now
		body.schedule = math.Max(now, body.schedule) + (float64(chunk)/body.rate)*1000
		if wait > 0 {
			if sleepErr := sleepWithCancel(time.Duration(wait*float64(time.Millisecond)), body.ctx); sleepErr != nil {
				return chunk, sleepErr
			}
		}
	}
	return chunk, err
}

func (body *pacedBody) Close() error {
	body.closed = true
	return body.reader.Close()
}

func (transport *MediaTransport) Close() { transport.client.CloseIdleConnections() }

func (transport *MediaTransport) Head(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error) {
	return transport.request(http.MethodHead, requestURL, headers, ctx)
}

func (transport *MediaTransport) Fetch(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error) {
	return transport.request(http.MethodGet, requestURL, headers, ctx)
}

func (transport *MediaTransport) request(method, requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error) {
	if transport.interval > 0 {
		transport.mu.Lock()
		now := time.Now()
		next := transport.lastFetch.Add(transport.interval)
		wait := time.Duration(0)
		if next.After(now) {
			wait = next.Sub(now)
		}
		transport.lastFetch = now.Add(wait)
		transport.mu.Unlock()
		if wait > 0 {
			if err := sleepWithCancel(wait, ctx); err != nil {
				return nil, err
			}
		}
	} else {
		transport.mu.Lock()
		transport.lastFetch = time.Now()
		transport.mu.Unlock()
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, contract.InputInvalid("下载地址无效")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := transport.client.Do(request)
	if err != nil {
		message := err.Error()
		if strings.Contains(message, "不允许重定向") {
			return nil, contract.InputInvalid("下载地址发生重定向")
		}
		return nil, contract.Unavailable(message)
	}
	if transport.rateBytes > 0 && response.Body != nil {
		response.Body = &pacedBody{
			ctx:      ctx,
			reader:   response.Body,
			schedule: float64(time.Now().UnixNano()) / 1e6,
			rate:     transport.rateBytes,
		}
	}
	return response, nil
}
