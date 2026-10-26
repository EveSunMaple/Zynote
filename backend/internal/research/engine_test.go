package research

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
)

func TestEngineCreateAndNote(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	knowledgeStore, err := knowledge.Open(filepath.Join(root, "knowledge_base"))
	if err != nil {
		t.Fatal(err)
	}
	defer knowledgeStore.Close()
	if _, err := knowledgeStore.Import(knowledge.ImportInput{
		Title: "机器学习", Text: "机器学习 特征工程 数据清洗", Locator: "https://example.com/a",
		SourceHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Category:   "课程", Origin: "local",
	}); err != nil {
		t.Fatal(err)
	}
	sources := NewSources(knowledgeStore, store.RootID)
	engine := &Engine{
		Store: store, Sources: sources, Connection: "conn",
		Model: func(context.Context, string, string) (string, error) {
			return "机器学习包括特征工程 [S1]", nil
		},
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "question": "机器学习的特征工程",
		"channels": []any{"library"}, "materials": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := created.(Task)
	if task.Status != "completed" || task.AnswerHash == nil || len(task.Sources) != 1 {
		t.Fatalf("task mismatch: %+v", task)
	}
	preview, err := engine.PreviewNote(task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SaveNote(task.TaskID, map[string]any{
		"kb_root_id": preview["kb_root_id"], "note_sha256": preview["note_sha256"],
		"sources_sha256": preview["sources_sha256"],
	}); err != nil {
		t.Fatal(err)
	}
	notes, err := store.Notes("", "active", 0)
	if err != nil || len(notes["entries"].([]any)) != 1 {
		t.Fatalf("notes mismatch: %+v %v", notes, err)
	}
	if _, err := engine.Create(context.Background(), map[string]any{
		"op": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "question": "无资料问题",
		"channels": []any{"bogus"}, "materials": []any{},
	}); err == nil {
		t.Fatal("external channel must fail explicitly")
	}
	_ = os.Remove(filepath.Join(root, "research-native.sqlite"))
}

func TestWorkflowCreateResumeExport(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	knowledgeStore, err := knowledge.Open(filepath.Join(root, "knowledge_base"))
	if err != nil {
		t.Fatal(err)
	}
	defer knowledgeStore.Close()
	if _, err := knowledgeStore.Import(knowledge.ImportInput{
		Title: "机器学习", Text: "机器学习 特征工程", Locator: "https://example.com/a",
		SourceHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Category:   "课程", Origin: "local",
	}); err != nil {
		t.Fatal(err)
	}
	sources := NewSources(knowledgeStore, store.RootID)
	catalog, err := sources.Catalog("", 0)
	if err != nil {
		t.Fatal(err)
	}
	items := catalog.(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("catalog mismatch: %+v", catalog)
	}
	bindingValue := items[0].(map[string]any)["binding"]
	engine := &Engine{
		Store: store, Sources: sources, Connection: "conn",
		Model: func(context.Context, string, string) (string, error) {
			return "依据原文，特征工程很重要 [S1]", nil
		},
	}
	created, err := engine.CreateWorkflow(context.Background(), map[string]any{
		"op": "cccccccccccccccccccccccccccccccc", "question": "特征工程",
		"materials": []any{bindingValue},
	})
	if err != nil {
		t.Fatal(err)
	}
	flow := created.(Workflow)
	resumed, err := engine.ActionWorkflow(context.Background(), map[string]any{
		"id": flow.WorkflowID, "op": "dddddddddddddddddddddddddddddddd",
		"revision": flow.Revision, "fence": flow.Fence, "kind": "resume", "retry_unknown": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := resumed.(Workflow)
	if completed.Status != "completed" || len(completed.Steps) != 2 ||
		completed.Steps[0].Status != "completed" || completed.Steps[1].Status != "completed" {
		t.Fatalf("workflow mismatch: %+v", completed)
	}
	if path, err := engine.ExportWorkflow(completed.WorkflowID); err != nil ||
		!filepath.IsAbs(path.(string)) {
		t.Fatalf("workflow export mismatch: %v %v", path, err)
	}
}

type externalRoundTrip func(*http.Request) (*http.Response, error)

func (fn externalRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestExternalSearchAndVerify(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	knowledgeStore, err := knowledge.Open(filepath.Join(root, "knowledge_base"))
	if err != nil {
		t.Fatal(err)
	}
	defer knowledgeStore.Close()
	engine := &Engine{
		Store: store, Sources: NewSources(knowledgeStore, store.RootID), Connection: "conn",
		External: &External{Client: &http.Client{Transport: externalRoundTrip(func(request *http.Request) (*http.Response, error) {
			body := `{"status":"ok","message":{"items":[{"DOI":"10.1000/xyz123","title":["特征工程方法"],"abstract":"摘要正文"}]}}`
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		})}},
		Model: func(context.Context, string, string) (string, error) {
			return "特征工程方法值得参考 [S1]", nil
		},
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "question": "特征工程",
		"channels": []any{"crossref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := created.(Task)
	if len(task.External) != 1 || task.External[0].Provider != "crossref" || len(task.Sources) != 1 {
		t.Fatalf("external task mismatch: %+v", task)
	}
	if _, err := engine.VerifyResult(task.TaskID); err != nil {
		t.Fatal(err)
	}
}
