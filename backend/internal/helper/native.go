package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type replyFrame struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type pendingItem struct {
	result chan json.RawMessage
	err    chan error
}

type Native struct {
	mu         sync.Mutex
	mode       string
	pdfiumPath string
	cacheDir   string
	embedModel string
	asrModel   string
	binaryPath string
	child      *exec.Cmd
	stdin      io.WriteCloser
	pending    sync.Map
	nextID     int64
}

func NewNative(mode string) *Native {
	return &Native{mode: mode}
}

func (n *Native) Configure(binaryPath, pdfiumPath, cacheDir, embedModel, asrModel string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.binaryPath = binaryPath
	n.pdfiumPath = pdfiumPath
	n.cacheDir = cacheDir
	n.embedModel = embedModel
	n.asrModel = asrModel
}

func (n *Native) ensure() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.child != nil {
		if n.child.Process != nil {
			return nil
		}
	}
	if n.binaryPath == "" {
		return fmt.Errorf("原生助手路径未配置")
	}
	args := []string{"--mode", n.mode}
	if n.mode == "pdf" && n.pdfiumPath != "" {
		args = append(args, "--pdfium", n.pdfiumPath)
	}
	if n.mode == "youtube" && n.cacheDir != "" {
		args = append(args, "--youtube-cache", n.cacheDir)
	}
	if n.mode == "embed" && n.embedModel != "" {
		args = append(args, "--embed-model", n.embedModel)
	}
	if n.mode == "asr" && n.asrModel != "" {
		args = append(args, "--asr-model", n.asrModel)
	}
	child := exec.Command(n.binaryPath, args...)
	stdin, err := child.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		return err
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		return err
	}
	n.child = child
	n.stdin = stdin
	go n.readLoop(stdout, child)
	return nil
}

func (n *Native) readLoop(stdout io.Reader, child *exec.Cmd) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 256*1024), 8*1024*1024)
	for scanner.Scan() {
		var frame replyFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			continue
		}
		if frame.Method != "" {
			continue
		}
		if frame.ID == "" {
			continue
		}
		value, ok := n.pending.Load(frame.ID)
		if !ok {
			continue
		}
		item := value.(*pendingItem)
		if frame.Error != nil {
			item.err <- fmt.Errorf("%s: %s", frame.Error.Code, frame.Error.Message)
		} else {
			item.result <- frame.Result
		}
	}
	_ = child.Wait()
	n.failAll("原生助手已退出")
}

func (n *Native) failAll(message string) {
	n.pending.Range(func(key, value any) bool {
		item := value.(*pendingItem)
		item.err <- fmt.Errorf("%s", message)
		n.pending.Delete(key)
		return true
	})
}

func (n *Native) Call(method string, params any, timeoutMS int) (json.RawMessage, error) {
	return n.CallContext(context.Background(), method, params, timeoutMS)
}

func (n *Native) CallContext(ctx context.Context, method string, params any, timeoutMS int) (json.RawMessage, error) {
	if err := n.ensure(); err != nil {
		return nil, err
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	id := fmt.Sprintf("h-%d", atomic.AddInt64(&n.nextID, 1))
	item := &pendingItem{result: make(chan json.RawMessage, 1), err: make(chan error, 1)}
	n.pending.Store(id, item)
	defer n.pending.Delete(id)
	frame := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": json.RawMessage(rawParams)}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	writer := n.stdin
	if writer == nil {
		n.mu.Unlock()
		return nil, fmt.Errorf("原生助手未启动")
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		n.mu.Unlock()
		return nil, err
	}
	n.mu.Unlock()
	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case result := <-item.result:
		return result, nil
	case err := <-item.err:
		return nil, err
	case <-ctx.Done():
		n.Dispose()
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("原生助手请求超时")
	}
}

func (n *Native) Dispose() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.child == nil {
		return
	}
	if n.stdin != nil {
		_ = n.stdin.Close()
	}
	if n.child.Process != nil {
		_ = n.child.Process.Kill()
	}
	n.child = nil
	slog.Info("native helper disposed", "mode", n.mode)
}
