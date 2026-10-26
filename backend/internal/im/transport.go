package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

func Loopback(input string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(input))
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("通讯组件只接受本机 127.0.0.1 端口")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

var localPathPattern = regexp.MustCompile(`^/[A-Za-z0-9_/-]+$`)

func LocalJSON(ctx context.Context, base, path string, body any, token string) (map[string]any, error) {
	origin, err := Loopback(base)
	if err != nil {
		return nil, err
	}
	if !localPathPattern.MatchString(path) || strings.Contains(path, "..") {
		return nil, fmt.Errorf("通讯操作路径无效")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("通讯请求参数无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("通讯请求失败")
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("通讯组件不允许跳转")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("本机通讯组件未确认请求")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("本机通讯组件未确认请求：%d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("通讯组件响应超过大小上限")
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, fmt.Errorf("通讯组件响应格式无效")
	}
	return value, nil
}

func PublicJSON(ctx context.Context, request *http.Request, limit int64) (map[string]any, error) {
	request.Header.Set("Accept", "application/json")
	client := &http.Client{
		Timeout: 40 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("通讯接口不允许跳转")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("通讯接口请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("通讯接口未成功")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, fmt.Errorf("通讯接口响应超过大小上限")
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, fmt.Errorf("通讯接口响应格式无效")
	}
	return value, nil
}
