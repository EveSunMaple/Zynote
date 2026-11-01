package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
)

// longSubtitle 生成一节约 2 小时、紧凑后远超首轮 10 万字预算的字幕；最后一句是只在后半节出现的“结尾原话”。
func longSubtitle(ending string) string {
	lines := []string{}
	count := 2400
	for index := 0; index < count; index++ {
		seconds := index * 3
		start := fmt.Sprintf("%02d:%02d:%02d,000", seconds/3600, seconds/60%60, seconds%60)
		end := fmt.Sprintf("%02d:%02d:%02d,900", seconds/3600, seconds/60%60, seconds%60)
		sentence := fmt.Sprintf("泰勒公式讲解第%d句，这里是课堂上的普通讲述内容，用来填充篇幅", index)
		if index == count-1 {
			sentence = ending
		}
		lines = append(lines, fmt.Sprintf("%d\n%s --> %s\n%s\n", index+1, start, end, sentence))
	}
	return strings.Join(lines, "\n")
}

func roundsEngine(t *testing.T, docs map[string]string, model func(round int, user string) string) (*Engine, *int) {
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
	knowledgeStore, err := knowledge.Open(filepath.Join(root, "knowledge_base"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { knowledgeStore.Close() })
	for title, body := range docs {
		sum := sha256.Sum256([]byte(body))
		if _, err := knowledgeStore.Import(knowledge.ImportInput{
			Title: title, Text: body, Locator: "https://example.com/" + hex.EncodeToString(sum[:4]),
			SourceHash: hex.EncodeToString(sum[:]), Category: "课程", Origin: "local",
		}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	engine := &Engine{
		Store: store, Sources: NewSources(knowledgeStore, store.RootID), Connection: "conn",
		Model: func(_ context.Context, _ string, user string) (string, error) {
			calls++
			return model(calls, user), nil
		},
	}
	return engine, &calls
}

func runRounds(t *testing.T, engine *Engine, question string) Task {
	t.Helper()
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("ab", 16), "question": question, "channels": []any{"library"}, "materials": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return createdTask(t, engine, created)
}

func TestParseRoundRequest(t *testing.T) {
	actions, requested := parseRoundRequest("```need\n[{\"action\":\"read\",\"source\":\"S1\",\"until\":\"end\"}]\n```")
	if !requested || len(actions) != 1 || actions[0].Source != "S1" {
		t.Fatalf("need block must parse: %+v %v", actions, requested)
	}
	if _, requested := parseRoundRequest("回答正文[S1]\n```need\n[]\n```"); requested {
		t.Fatal("an answer that merely contains a need block is still an answer")
	}
	if actions, requested := parseRoundRequest("```need\n[坏掉\n```"); !requested || len(actions) != 0 {
		t.Fatal("broken need block is a request with no actions")
	}
}

func TestViewOffsetUntilCutsAtLine(t *testing.T) {
	view := []rune("[00:00:01]甲\n[00:00:05]乙\n[00:00:09]丙\n")
	if got := string(view[:viewOffsetUntil(view, 5)]); got != "[00:00:01]甲\n[00:00:05]乙\n" {
		t.Fatalf("got %q", got)
	}
}

// 长课首轮只读到前半节；模型请求继续读到结尾后，第二轮能看到并逐字引用最后一句。
func TestRoundsReadRestOfTruncatedLecture(t *testing.T) {
	ending := "所以拉格朗日余项一定要记住它的中间点"
	engine, calls := roundsEngine(t, map[string]string{"泰勒公式长课": longSubtitle(ending)}, func(round int, user string) string {
		if round == 1 {
			if strings.Contains(user, ending) {
				t.Error("first round must not already contain the ending")
			}
			return "```need\n[{\"action\":\"read\",\"source\":\"S1\",\"until\":\"end\"}]\n```"
		}
		if !strings.Contains(user, ending) {
			t.Error("second round must contain the newly read ending")
		}
		return "老师最后强调“" + ending + "”[S1@01:59:57]。"
	})
	task := runRounds(t, engine, "泰勒公式 拉格朗日余项")
	if task.Status != "completed" || *calls != 2 {
		t.Fatalf("expected completion after 2 rounds, got %s after %d (%s)", task.Status, *calls, task.ErrorCode)
	}
	if task.Sources[0].Truncated || len(task.Quotes) != 1 || task.Quotes[0].Status != "exact" {
		t.Fatalf("rest of lecture must be read and quote verified: truncated=%v quotes=%+v", task.Sources[0].Truncated, task.Quotes)
	}
	if !hasEvent(task, "第 1 轮：继续读取《泰勒公式长课》") {
		t.Fatalf("round event missing: %+v", task.Events)
	}
	if err := verifyLocalTaskSources(&task); err != nil {
		t.Fatalf("extended reading must still be a verifiable prefix: %v", err)
	}
}

// 模型请求在知识库再检索时，新资料加入阅读清单并可被引用。
func TestRoundsSearchLibraryAddsSource(t *testing.T) {
	docs := map[string]string{"泰勒公式": "泰勒公式 用多项式逼近函数", "洛必达法则": "洛必达法则 处理零比零未定式的极限"}
	engine, _ := roundsEngine(t, docs, func(round int, user string) string {
		if round == 1 {
			return "```need\n[{\"action\":\"search_library\",\"query\":\"洛必达法则\"}]\n```"
		}
		if !strings.Contains(user, "零比零未定式") {
			t.Error("second round must contain the newly found source")
		}
		return "泰勒公式用多项式逼近函数[S1]，洛必达法则处理零比零未定式[S2]。"
	})
	task := runRounds(t, engine, "泰勒公式")
	if task.Status != "completed" || len(task.Materials) != 2 {
		t.Fatalf("expected two materials after search, got %s %d (%s)", task.Status, len(task.Materials), task.ErrorCode)
	}
}

// 模型一直请求资料也必定停止：无进展的下一轮强制作答，仍不作答则失败，而不是无限循环。
func TestRoundsStopWhenModelKeepsAsking(t *testing.T) {
	engine, calls := roundsEngine(t, map[string]string{"泰勒公式": "泰勒公式 用多项式逼近函数"}, func(int, string) string {
		return "```need\n[{\"action\":\"search_library\",\"query\":\"泰勒公式\"}]\n```"
	})
	_, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("cd", 16), "question": "泰勒公式", "channels": []any{"library"}, "materials": []any{},
	})
	if err == nil || !strings.Contains(err.Error(), "最后一轮") || *calls > maxResearchRounds {
		t.Fatalf("must stop within %d rounds, got %v after %d calls", maxResearchRounds, err, *calls)
	}
}

func hasEvent(task Task, prefix string) bool {
	for _, event := range task.Events {
		if strings.HasPrefix(text(event["message"]), prefix) {
			return true
		}
	}
	return false
}

// 第二轮的提示里要列出已做过的补充，避免模型原样重复。
func TestRoundGuideListsHistory(t *testing.T) {
	task := &Task{Sources: []TaskSource{}, Events: []map[string]any{roundEvent(1, "知识库检索“泰勒”没有新资料")}}
	guide := roundGuide(2, task, roundOptions{library: true})
	if !strings.Contains(guide, "已经做过的补充") || !strings.Contains(guide, "第 1 轮：知识库检索“泰勒”没有新资料") {
		t.Fatalf("guide must list history: %s", guide)
	}
}
