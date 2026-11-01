package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
)

// pickerStub 记录 shell.pickFiles 的参数；release 为空时立即应答，否则阻塞到通道关闭。
type pickerStub struct {
	mu      sync.Mutex
	calls   int
	method  string
	params  map[string]any
	timeout int
	reply   json.RawMessage
	err     error
	release chan struct{}
}

func (stub *pickerStub) Call(method string, params any, timeoutMS int) (json.RawMessage, error) {
	stub.mu.Lock()
	stub.calls++
	stub.method, stub.timeout = method, timeoutMS
	stub.params, _ = params.(map[string]any)
	release := stub.release
	stub.mu.Unlock()
	if release != nil {
		<-release
	}
	return stub.reply, stub.err
}

func (stub *pickerStub) callCount() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.calls
}

type pickerStore struct{ reverse contract.ReverseCaller }

func (store pickerStore) State() *contract.State          { return nil }
func (store pickerStore) SetState(*contract.State)        {}
func (store pickerStore) Reverse() contract.ReverseCaller { return store.reverse }

func pickedPaths(paths ...string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"paths": paths})
	return raw
}

func TestPickKnowledgeFilesSendsPickerOptions(t *testing.T) {
	stub := &pickerStub{reply: pickedPaths("a.txt", "  ", "b.md")}
	paths, err := pickKnowledgeFiles(context.Background(), stub, "D:/Library")
	if err != nil || strings.Join(paths, "|") != "a.txt|b.md" {
		t.Fatalf("picked paths mismatch: %v %v", paths, err)
	}
	extensions, _ := stub.params["extensions"].([]string)
	if stub.method != "shell.pickFiles" || stub.params["title"] != "导入知识库资料" || stub.params["defaultPath"] != "D:/Library" ||
		strings.Join(extensions, ",") != "pdf,docx,txt,md,srt" {
		t.Fatalf("picker params mismatch: %s %+v", stub.method, stub.params)
	}
	if stub.timeout < 60*60*1000 {
		t.Fatalf("interactive picker must not use a short timeout: %d", stub.timeout)
	}
	if _, err := pickKnowledgeFiles(context.Background(), stub, ""); err != nil || stub.params["defaultPath"] != nil {
		t.Fatalf("empty library root should omit defaultPath: %+v %v", stub.params, err)
	}
}

func TestPickKnowledgeFilesReportsPickerFailures(t *testing.T) {
	many := make([]string, knowledgeImportMaxFiles+1)
	for index := range many {
		many[index] = fmt.Sprintf("%d.txt", index)
	}
	cases := map[string]struct {
		stub *pickerStub
		want string
	}{
		"reverse error": {&pickerStub{err: errors.New("shell service shell.pickFiles timeout")}, "timeout"},
		"invalid reply": {&pickerStub{reply: json.RawMessage(`"oops"`)}, "文件选择器响应无效"},
		"too many":      {&pickerStub{reply: pickedPaths(many...)}, "一次最多导入 200 份资料（本次选择了 201 份）"},
	}
	for name, item := range cases {
		paths, err := pickKnowledgeFiles(context.Background(), item.stub, "")
		if err == nil || paths != nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s: expected %q, got %v %v", name, item.want, paths, err)
		}
	}
	if paths, err := pickKnowledgeFiles(context.Background(), &pickerStub{reply: pickedPaths()}, ""); err != nil || len(paths) != 0 {
		t.Fatalf("cancelled picker should return no paths: %v %v", paths, err)
	}
}

func TestKnowledgeImportPickerRunsInsideCancellableJob(t *testing.T) {
	invoker := NewInvoker()
	state := &contract.State{DataRoot: t.TempDir(), LibraryRoot: t.TempDir()}
	t.Cleanup(invoker.Close)
	stub := &pickerStub{reply: pickedPaths(), release: make(chan struct{})}
	defer close(stub.release)
	params := json.RawMessage(`{"category":"牙体牙髓病学"}`)
	done := make(chan error, 1)
	go func() {
		_, err := invoker.handleKnowledgeImport(pickerStore{stub}, state, params)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for invoker.jobs.Current() == nil || stub.callCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("picker should open inside a visible job")
		}
		time.Sleep(5 * time.Millisecond)
	}

	second := &pickerStub{reply: pickedPaths()}
	if _, err := invoker.handleKnowledgeImport(pickerStore{second}, state, params); err == nil || second.callCount() != 0 {
		t.Fatalf("a running job must reject before opening another picker: %v calls=%d", err, second.callCount())
	}

	invoker.jobs.CancelAll()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "任务已取消") {
			t.Fatalf("cancel while picking should stop the job: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel should not wait for the picker to close")
	}
	if invoker.jobs.Current() != nil {
		t.Fatal("job should be released after cancel")
	}
}

func TestReadKnowledgeFileSizeAndLengthLimits(t *testing.T) {
	directory := t.TempDir()
	cases := map[string]struct {
		size int64
		want error
	}{
		"大.txt":  {knowledgeTextMaxBytes + 1, errKnowledgeTextTooLarge},
		"大.docx": {knowledgeImportMaxBytes + 1, errKnowledgeFileTooLarge},
		"大.pdf":  {knowledgeImportMaxBytes + 1, errKnowledgeFileTooLarge},
	}
	for name, item := range cases {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, item.size); err != nil {
			t.Fatal(err)
		}
		if _, err := readKnowledgeFile(nil, path); !errors.Is(err, item.want) {
			t.Fatalf("%s should be rejected before reading: %v", name, err)
		}
	}
	long := filepath.Join(directory, "长.txt")
	if err := os.WriteFile(long, []byte(strings.Repeat("a", knowledge.MaxTextRunes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readKnowledgeFile(nil, long); !errors.Is(err, errKnowledgeTextTooLarge) {
		t.Fatalf("text over the rune limit should get a clear error: %v", err)
	}
}

// pdfStub 模拟原生 PDF 解析；mutate 非空时在解析期间改写文件。
type pdfStub struct {
	reply  string
	mutate string
}

func (stub pdfStub) Call(method string, params any, _ int) (json.RawMessage, error) {
	if stub.mutate != "" {
		_ = os.WriteFile(stub.mutate, []byte("%PDF-1.4 changed"), 0o644)
	}
	return json.RawMessage(stub.reply), nil
}

func TestReadKnowledgeFilePdfResults(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "讲义.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input, err := readKnowledgeFile(pdfStub{reply: `{"text":"根尖周炎","needsOcr":false}`}, path)
	if err != nil || input.Text != "根尖周炎" || input.Title != "讲义.pdf" {
		t.Fatalf("pdf import mismatch: %+v %v", input, err)
	}
	if _, err := readKnowledgeFile(pdfStub{reply: `{"text":"","needsOcr":true}`}, path); !errors.Is(err, errKnowledgeNeedsOcr) {
		t.Fatalf("image pdf should ask for OCR: %v", err)
	}
	if _, err := readKnowledgeFile(pdfStub{reply: `{"text":"x"}`, mutate: path}, path); !errors.Is(err, errKnowledgeFileChanged) {
		t.Fatalf("file changed during parsing should be rejected: %v", err)
	}
}

func TestReadKnowledgeFileFollowsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "原文.md")
	if err := os.WriteFile(target, []byte("# 同步盘里的笔记"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "链接.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not permitted here: %v", err)
	}
	input, err := readKnowledgeFile(nil, link)
	if err != nil || input.Locator != link || input.Title != "链接.md" || !strings.Contains(input.Text, "同步盘") {
		t.Fatalf("symlink import mismatch: %+v %v", input, err)
	}
	folderLink := filepath.Join(directory, "目录链接.md")
	if err := os.Symlink(t.TempDir(), folderLink); err != nil {
		t.Skipf("symlink not permitted here: %v", err)
	}
	if _, err := readKnowledgeFile(nil, folderLink); !errors.Is(err, errKnowledgeFileUnreadable) || !strings.Contains(err.Error(), "普通文件") {
		t.Fatalf("symlink to a folder should be rejected clearly: %v", err)
	}
}
