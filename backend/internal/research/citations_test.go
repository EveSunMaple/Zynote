package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
)

func sampleSRT(lines ...string) string {
	blocks := []string{}
	for index, line := range lines {
		start := index * 10
		blocks = append(blocks, fmt.Sprintf("%d\n%s,000 --> %s,500\n%s", index+1, clockText(start), clockText(start+9), line))
	}
	return strings.Join(blocks, "\n\n")
}

func citationEngine(t *testing.T, texts []string) (*Engine, []any) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	kb, err := knowledge.Open(filepath.Join(root, "knowledge_base"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kb.Close() })
	for index, value := range texts {
		sum := sha256.Sum256([]byte(value))
		if _, err := kb.Import(knowledge.ImportInput{
			Title: fmt.Sprintf("课%d", index+1), Text: value, Locator: fmt.Sprintf("https://example.com/%d", index),
			SourceHash: hex.EncodeToString(sum[:]), Category: "课程", Origin: "local",
		}); err != nil {
			t.Fatal(err)
		}
	}
	sources := NewSources(kb, store.RootID)
	catalog, err := sources.Catalog("", 0)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []any{}
	for _, item := range catalog.(map[string]any)["items"].([]any) {
		bindings = append(bindings, item.(map[string]any)["binding"])
	}
	return &Engine{Store: store, Sources: sources, Connection: "conn"}, bindings
}

func TestSourceViewCompactsSubtitles(t *testing.T) {
	view := sourceView(sampleSRT("第一句", "第二句", "第三句"))
	want := "[00:00:00]第一句\n[00:00:10]第二句\n[00:00:20]第三句"
	if view != want {
		t.Fatalf("compact view mismatch:\n%s", view)
	}
	if plain := "普通讲义正文"; sourceView(plain) != plain {
		t.Fatal("non-subtitle text must stay unchanged")
	}
}

func TestAllocateBudgetSharesFairly(t *testing.T) {
	caps := allocateBudget([]int{10, 500, 500}, 600)
	if caps[0] != 10 || caps[1] != 295 || caps[2] != 295 {
		t.Fatalf("caps mismatch: %v", caps)
	}
}

func TestValidCitationsAcceptTimedForms(t *testing.T) {
	sources := []TaskSource{{SourceID: "S1", Read: true}, {SourceID: "S2", Read: true}}
	for _, note := range []string{"结论 [S1]", "“原话”[S1@00:14:06]", "原话 [S1, 14:06-14:22]", "对比 [S1, S2]", "原话【S2】"} {
		if !validResearchCitations(note, sources) {
			t.Fatalf("should accept %q", note)
		}
	}
	for _, note := range []string{"没有引用", "越界 [S3]", "越界 [S1, S3]"} {
		if validResearchCitations(note, sources) {
			t.Fatalf("should reject %q", note)
		}
	}
}

func TestCheckQuotesClassifiesEachQuote(t *testing.T) {
	text := sampleSRT("我们先看佩亚诺余项", "它是一个高阶无穷小啊", "所以误差估计的时候就不大方便", "另外一种就是拉格朗日余项",
		"这个余项呢就稍微来的准确一点", "下面我们做一道例题")
	sources := []TaskSource{{SourceID: "S1", Read: true, Text: sourceView(text)}}
	note := strings.Join([]string{
		"“它是一个高阶无穷小”[S1@00:00:10]",        // 去掉语气词后逐字一致
		"“所以误差估计的时候不大方便”[S1@00:00:20]",    // 有删改（少一个“就”）
		"“另外一种就是拉格朗日余项”[S1@00:00:50]",     // 原话在 00:00:30，时间注错但在窗口内
		"“下面我们做一道例题”[S1@00:30:00]",        // 时间差太远
		"“佩亚诺余项考试必考一定要背下来”[S1@00:00:00]",  // 捏造
		"“这个余项呢……稍微来的准确一点”（00:00:40）[S1]", // 省略号拼接 + 时间写在括号里
	}, "\n")
	checks := CheckQuotes(note, sources)
	want := []string{"exact", "edited", "exact", "moved", "unverified", "exact"}
	if len(checks) != len(want) {
		t.Fatalf("checks mismatch: %+v", checks)
	}
	for index, check := range checks {
		if check.Status != want[index] {
			t.Fatalf("quote %d %q: got %s want %s (%+v)", index, check.Quote, check.Status, want[index], check)
		}
	}
	if checks[3].At != "00:00:50" || checks[0].Original != "它是一个高阶无穷小啊" {
		t.Fatalf("location mismatch: %+v", checks)
	}
}

func TestResearchMarksFabricatedQuoteAndKeepsAnswer(t *testing.T) {
	engine, bindings := citationEngine(t, []string{sampleSRT("今天讲泰勒公式", "余项有两种形式", "先看佩亚诺余项")})
	engine.Model = func(context.Context, string, string) (string, error) {
		return "老师说“余项有两种形式”[S1@00:00:10]，还说“佩亚诺余项考试必考一定要背下来”[S1@00:00:20]。", nil
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("1", 32), "question": "余项", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := created.(Task)
	if task.Status != "completed" || len(task.Quotes) != 2 || task.Quotes[0].Status != "exact" ||
		task.Quotes[1].Status != "unverified" || !strings.Contains(task.Limitations, "1 条引文未能在原文中找到") {
		t.Fatalf("quote result mismatch: %+v / %s", task.Quotes, task.Limitations)
	}
	if _, err := engine.VerifyResult(task.TaskID); err != nil {
		t.Fatal(err)
	}
}

func TestResearchReadsEveryLongMaterial(t *testing.T) {
	texts := []string{}
	for index := 0; index < 5; index++ {
		lines := []string{}
		for line := 0; line < 1500; line++ {
			lines = append(lines, fmt.Sprintf("第%d课第%d句讲解例题的过程", index+1, line))
		}
		texts = append(texts, sampleSRT(lines...))
	}
	engine, bindings := citationEngine(t, texts)
	var prompt string
	engine.Model = func(_ context.Context, _ string, user string) (string, error) {
		prompt = user
		return "结论 [S1]", nil
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("2", 32), "question": "比较五节课", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := created.(Task)
	if len(task.Sources) != 5 || !strings.Contains(prompt, "<source id=\"S5\">") {
		t.Fatalf("every material must be read: %d sources", len(task.Sources))
	}
	if !task.Sources[0].Truncated || !strings.Contains(task.Sources[0].Coverage, "只读到") ||
		!strings.Contains(task.Limitations, "只读到") || !strings.Contains(prompt, "未读部分不得断言") {
		t.Fatalf("coverage must be explicit: %+v / %s", task.Sources[0].Coverage, task.Limitations)
	}
	if _, err := engine.VerifyResult(task.TaskID); err != nil {
		t.Fatalf("verify must pass with five materials: %v", err)
	}
	if _, err := engine.Export(task.TaskID); err != nil {
		t.Fatalf("export must pass with five materials: %v", err)
	}
}

func TestSearchSendsWholeLongLecture(t *testing.T) {
	lines := []string{}
	for line := 0; line < 2500; line++ {
		lines = append(lines, "老师讲解例题的过程")
	}
	lines = append(lines, "拉格朗日余项的适用条件是函数n加1阶可导")
	engine, _ := citationEngine(t, []string{sampleSRT(lines...)})
	var prompt string
	engine.Model = func(_ context.Context, _ string, user string) (string, error) {
		prompt = user
		return "答 [S1]", nil
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("3", 32), "question": "拉格朗日余项", "channels": []any{"library"}, "materials": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := created.(Task)
	if task.Sources[0].Truncated || !strings.Contains(prompt, "拉格朗日余项的适用条件") ||
		!strings.Contains(task.Sources[0].Coverage, "已读整节") {
		t.Fatalf("late content must reach the model: %+v", task.Sources[0].Coverage)
	}
}

func TestRejectedAnswerIsKeptForDiagnosis(t *testing.T) {
	engine, bindings := citationEngine(t, []string{sampleSRT("一", "二", "三")})
	engine.Model = func(context.Context, string, string) (string, error) { return "没有任何引用的回答", nil }
	_, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("4", 32), "question": "问题", "channels": []any{"library"}, "materials": bindings,
	})
	if err == nil {
		t.Fatal("answer without citation must be rejected")
	}
	rows := engine.List()
	if text(rows[0].(map[string]any)["rejected_answer"]) != "没有任何引用的回答" {
		t.Fatalf("rejected answer must be kept: %+v", rows[0])
	}
}

func TestTaskListOmitsSourceText(t *testing.T) {
	lines := []string{}
	for line := 0; line < 3000; line++ {
		lines = append(lines, "老师讲解例题的过程与要点")
	}
	engine, bindings := citationEngine(t, []string{sampleSRT(lines...)})
	engine.Model = func(context.Context, string, string) (string, error) {
		return "“老师讲解例题的过程与要点”[S1@00:00:10]", nil
	}
	if _, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("5", 32), "question": "问题", "channels": []any{"library"}, "materials": bindings,
	}); err != nil {
		t.Fatal(err)
	}
	row := engine.List()[0].(map[string]any)
	encoded := string(mustJSON(row))
	if len([]rune(encoded)) > 5000 || row["materials"] != nil || row["quotes"] == nil {
		t.Fatalf("list row must be a summary: %d chars", len([]rune(encoded)))
	}
	source := row["sources"].([]any)[0].(map[string]any)
	if source["text"] != nil || source["coverage"] == nil {
		t.Fatalf("source summary mismatch: %+v", source)
	}
}

func TestRefreshDuringRunKeepsTaskRunning(t *testing.T) {
	engine, bindings := citationEngine(t, []string{sampleSRT("一", "二", "三")})
	release, entered, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	engine.Model = func(context.Context, string, string) (string, error) {
		close(entered)
		<-release
		return "答 [S1]", nil
	}
	go func() {
		defer close(done)
		_, _ = engine.Create(context.Background(), map[string]any{
			"op": strings.Repeat("6", 32), "question": "问题", "channels": []any{"library"}, "materials": bindings,
		})
	}()
	<-entered
	other := &Engine{Store: engine.Store, Sources: engine.Sources, Connection: "conn"}
	status := text(other.List()[0].(map[string]any)["status"])
	close(release)
	<-done
	if status != "running" {
		t.Fatalf("a refresh during the run must not mark it interrupted: %s", status)
	}
}

func TestExportIncludesQuoteChecks(t *testing.T) {
	engine, bindings := citationEngine(t, []string{sampleSRT("今天讲泰勒公式", "余项有两种形式", "先看佩亚诺余项")})
	engine.Model = func(context.Context, string, string) (string, error) {
		return "老师说“余项有两种形式”[S1@00:00:10]，又说“这句话老师没说过”[S1@00:00:20]。", nil
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("7", 32), "question": "余项", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := engine.Export(created.(Task).TaskID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path.(string))
	if !strings.Contains(string(body), "【原文一致】“余项有两种形式”") || !strings.Contains(string(body), "【待核对】“这句话老师没说过”") {
		t.Fatalf("export must list quote checks:\n%s", body)
	}
}
