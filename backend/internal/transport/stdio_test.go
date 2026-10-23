package transport

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

type reverseHandler struct {
	server *Server
	done   chan error
}

func (handler *reverseHandler) Invoke(method string, params json.RawMessage) (any, error) {
	if method != "client.trigger" {
		return map[string]any{"ok": true}, nil
	}
	raw, err := handler.server.Call("client.echo", map[string]any{"value": "x"}, 1000)
	handler.done <- err
	return map[string]any{"reply": string(raw)}, nil
}

func TestReverseCallDoesNotDeadlock(t *testing.T) {
	input, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	handler := &reverseHandler{done: make(chan error, 1)}
	server := NewServer(handler)
	handler.server = server
	go func() { _ = server.Serve(context.Background(), input, outputWriter) }()
	encoder := json.NewEncoder(inputWriter)
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "c-1", "method": "client.trigger"}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(outputReader)
	frame := map[string]any{}
	if err := decoder.Decode(&frame); err != nil {
		t.Fatal(err)
	}
	if frame["method"] != "client.echo" {
		t.Fatalf("expected reverse request: %+v", frame)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": frame["id"], "result": map[string]any{"value": "y"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-handler.done:
		if err != nil {
			t.Fatalf("reverse call failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverse call blocked the request loop")
	}
	reply := map[string]any{}
	if err := decoder.Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if reply["id"] != "c-1" {
		t.Fatalf("reply mismatch: %+v", reply)
	}
	_ = inputWriter.Close()
	_ = outputReader.Close()
}

func TestServeRejectsBadFrames(t *testing.T) {
	server := NewServer(&reverseHandler{})
	if err := server.Serve(context.Background(), strings.NewReader("{}\n"), io.Discard); err != nil {
		t.Fatalf("bad frame should not stop server: %v", err)
	}
}
