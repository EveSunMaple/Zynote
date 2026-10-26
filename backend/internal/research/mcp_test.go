package research

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func filepathEval(path string) (string, error) { return filepath.EvalSymlinks(path) }

func TestMcpDiscoverAndCall(t *testing.T) {
	root := t.TempDir()
	store, err := Open(mustEval(t, root))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var mu sync.Mutex
	calls := 0
	transport := externalRoundTrip(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if request.Method == "DELETE" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		body, _ := io.ReadAll(request.Body)
		var packet map[string]any
		_ = json.Unmarshal(body, &packet)
		if packet["method"] == nil {
			return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		method := text(packet["method"])
		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "search", "description": "只读检索", "inputSchema": map[string]any{
					"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}},
					"required": []any{"query"}, "additionalProperties": false,
				},
				"annotations": map[string]any{"readOnlyHint": true},
			}}}
		case "tools/call":
			calls++
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "未核实线索"}}}
		default:
			result = map[string]any{}
		}
		encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": packet["id"], "result": result})
		return &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "Mcp-Session-Id": []string{"session-1"}},
			Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request,
		}, nil
	})
	factory := func(endpoint string) *McpSession {
		return &McpSession{Endpoint: endpoint, Client: &http.Client{Transport: transport}}
	}
	mcp := &Mcp{Store: store, Session: factory}
	changed, err := mcp.Change(map[string]any{
		"op": "11111111111111111111111111111111", "revision": 0,
		"change": map[string]any{
			"kind": "server", "id": nil, "name": "检索", "endpoint": "https://mcp.example.com/mcp",
			"enabled": false, "allowed_tools": []any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog := changed.(McpCatalog)
	serverID := catalog.Servers[0].ID
	discovered, err := mcp.Discover(context.Background(), map[string]any{
		"op": "22222222222222222222222222222222", "id": serverID, "revision": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog = discovered.(McpCatalog)
	if len(catalog.Servers[0].Tools) != 1 || !catalog.Servers[0].Tools[0].ReadOnlyDeclared {
		t.Fatalf("mcp tools mismatch: %+v", catalog)
	}
	enabled, err := mcp.Change(map[string]any{
		"op": "33333333333333333333333333333333", "revision": 2,
		"change": map[string]any{
			"kind": "server", "id": serverID, "name": "检索", "endpoint": "https://mcp.example.com/mcp",
			"enabled": true, "allowed_tools": []any{"search"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog = enabled.(McpCatalog)
	snapshot, err := mcp.Snapshot(map[string]any{
		"revision": catalog.Revision,
		"items":    []any{map[string]any{"server_id": serverID, "tool": "search"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mcp.Verify(snapshot); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	snapshotMap := map[string]any{}
	_ = json.Unmarshal(encoded, &snapshotMap)
	result, err := mcp.Call(context.Background(), map[string]any{
		"op": "44444444444444444444444444444444", "taskId": "55555555555555555555555555555555",
		"snapshot": snapshotMap, "serverId": serverID, "tool": "search",
		"arguments": map[string]any{"query": "特征工程"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "未核实线索" || result.Status != "completed" || calls != 1 {
		t.Fatalf("mcp result mismatch: %+v %d", result, calls)
	}
	again, err := mcp.Call(context.Background(), map[string]any{
		"op": "44444444444444444444444444444444", "taskId": "55555555555555555555555555555555",
		"snapshot": snapshotMap, "serverId": serverID, "tool": "search",
		"arguments": map[string]any{"query": "特征工程"},
	})
	if err != nil || again.Text != result.Text || calls != 1 {
		t.Fatalf("mcp replay mismatch: %+v %v %d", again, err, calls)
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	value, err := filepathEval(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
