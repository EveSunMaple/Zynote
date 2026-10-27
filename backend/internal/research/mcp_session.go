package research

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const mcpSessionHeader = "MCP-Session-Id"

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

type McpSession struct {
	Endpoint string
	Client   *http.Client
	sequence int
	session  string
	version  string
	ready    bool
}

func NewMcpSession(endpoint string) *McpSession {
	normalized, err := mcpEndpoint(endpoint)
	if err != nil {
		normalized = endpoint
	}
	return &McpSession{Endpoint: normalized, Client: NewExternal().Client}
}

func (session *McpSession) headers(version string) http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json, text/event-stream")
	header.Set("Accept-Encoding", "identity")
	if session.session != "" {
		header.Set(mcpSessionHeader, session.session)
	}
	if version != "" {
		header.Set("MCP-Protocol-Version", version)
	}
	return header
}

func mcpPostError(status int, hasSession bool) error {
	switch status {
	case 429:
		return fmt.Errorf("MCP 服务已限流，不自动重试")
	case 401, 403:
		return fmt.Errorf("MCP 服务需要身份验证，公开连接不支持密钥")
	case 404:
		if hasSession {
			return fmt.Errorf("MCP 会话已过期，不自动重放调用")
		}
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("MCP 服务返回 HTTP %d", status)
	}
	return nil
}

func (session *McpSession) validateSessionHeader(response *http.Response) error {
	value := response.Header.Get(mcpSessionHeader)
	if value == "" {
		return nil
	}
	if !regexp.MustCompile(`^[\x21-\x7e]{1,512}$`).MatchString(value) {
		return fmt.Errorf("MCP 会话编号无效")
	}
	if session.session != "" && session.session != value {
		return fmt.Errorf("MCP 会话发生改变")
	}
	return nil
}

func (session *McpSession) post(ctx context.Context, body []byte, version string) (*http.Response, error) {
	if len(body) > 32768 {
		return nil, fmt.Errorf("MCP 请求参数超过大小上限")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, session.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("MCP 请求失败")
	}
	request.Header = session.headers(version)
	client := session.Client
	if client == nil {
		client = NewExternal().Client
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("MCP 请求失败")
	}
	if err := mcpPostError(response.StatusCode, session.session != ""); err != nil {
		response.Body.Close()
		return nil, err
	}
	if err := session.validateSessionHeader(response); err != nil {
		response.Body.Close()
		return nil, err
	}
	return response, nil
}

func readMCPBody(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 262144+1))
	if err != nil || len(raw) > 262144 {
		return nil, fmt.Errorf("MCP 响应超过大小上限")
	}
	return raw, nil
}

func parseMCPEventStream(raw []byte) ([]map[string]any, error) {
	messages := []map[string]any{}
	events := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n")
	for _, event := range events {
		data := []string{}
		for _, line := range strings.Split(event, "\n") {
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(data) == 0 {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(strings.Join(data, "\n")), &value) != nil {
			return nil, fmt.Errorf("MCP 响应格式不支持")
		}
		messages = append(messages, value)
		if len(messages) > 128 {
			return nil, fmt.Errorf("MCP 响应消息过多")
		}
	}
	return messages, nil
}

func readMCPMessages(response *http.Response) ([]map[string]any, error) {
	contentType := strings.Split(response.Header.Get("Content-Type"), ";")[0]
	switch strings.TrimSpace(contentType) {
	case "application/json":
		raw, err := readMCPBody(response.Body)
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil {
			return nil, fmt.Errorf("MCP 服务响应格式不支持")
		}
		return []map[string]any{value}, nil
	case "text/event-stream":
		raw, err := readMCPBody(response.Body)
		if err != nil {
			return nil, err
		}
		return parseMCPEventStream(raw)
	}
	return nil, fmt.Errorf("MCP 服务响应格式不支持")
}

func matchingMCPReply(messages []map[string]any, id int) (map[string]any, error) {
	if len(messages) > 128 {
		return nil, fmt.Errorf("MCP 响应消息过多")
	}
	matching := []map[string]any{}
	for _, message := range messages {
		if text(message["method"]) != "" {
			if message["id"] != nil {
				return nil, fmt.Errorf("MCP 服务请求客户端执行额外操作，本次未执行")
			}
			continue
		}
		if int(number(message["id"])) == id {
			matching = append(matching, message)
		}
	}
	if len(matching) != 1 {
		return nil, fmt.Errorf("MCP 响应不完整或请求编号不一致")
	}
	return matching[0], nil
}

func (session *McpSession) Request(ctx context.Context, method string, params any) (any, error) {
	session.sequence++
	id := session.sequence
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, fmt.Errorf("MCP 请求参数无效")
	}
	response, err := session.post(ctx, body, session.version)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if header := response.Header.Get(mcpSessionHeader); header != "" && method == "initialize" {
		session.session = header
	}
	messages, err := readMCPMessages(response)
	if err != nil {
		return nil, err
	}
	reply, err := matchingMCPReply(messages, id)
	if err != nil {
		return nil, err
	}
	if errorValue, ok := reply["error"].(map[string]any); ok && errorValue != nil {
		return nil, fmt.Errorf("MCP 服务拒绝请求，不自动重试")
	}
	result, ok := reply["result"]
	if !ok {
		return nil, fmt.Errorf("MCP 响应缺少结果")
	}
	return result, nil
}

func (session *McpSession) Initialize(ctx context.Context) error {
	if session.ready {
		return nil
	}
	raw, err := session.Request(ctx, "initialize", map[string]any{
		"protocolVersion": mcpVersions[0], "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "Zynote-Native", "version": "1"},
	})
	if err != nil {
		return err
	}
	value, _ := raw.(map[string]any)
	version := text(value["protocolVersion"])
	supported := false
	for _, candidate := range mcpVersions {
		if candidate == version {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("MCP 协议版本不受支持")
	}
	session.version = version
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	response, err := session.post(ctx, body, version)
	if err != nil {
		return fmt.Errorf("MCP 初始化确认失败")
	}
	defer response.Body.Close()
	response.Body.Close()
	session.ready = true
	return nil
}

func (session *McpSession) Tools(ctx context.Context) ([]McpTool, error) {
	if err := session.Initialize(ctx); err != nil {
		return nil, err
	}
	list := []any{}
	cursor := ""
	for page := 0; page < 5; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := session.Request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		value, _ := raw.(map[string]any)
		items, _ := value["tools"].([]any)
		list = append(list, items...)
		if len(list) > 64 {
			return nil, fmt.Errorf("MCP 工具目录超过64项")
		}
		next := text(value["nextCursor"])
		if next == "" {
			return mcpTools(list)
		}
		if next == cursor {
			return nil, fmt.Errorf("MCP 工具分页没有进展")
		}
		cursor = next
	}
	return nil, fmt.Errorf("MCP 工具目录分页超过本次范围，未把部分目录当作完整目录")
}

func (session *McpSession) Close() error {
	if session.session == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, session.Endpoint, nil)
	if err == nil {
		request.Header.Set(mcpSessionHeader, session.session)
		request.Header.Set("MCP-Protocol-Version", session.version)
		client := session.Client
		if client == nil {
			client = NewExternal().Client
		}
		if response, err := client.Do(request); err == nil {
			response.Body.Close()
		}
	}
	session.session = ""
	session.ready = false
	return nil
}
