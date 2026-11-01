package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
	"github.com/EveSunMaple/Zynote/backend/internal/textbook"
	"github.com/EveSunMaple/Zynote/backend/internal/zhiyun"
)

func TestReplayCommentsRetainASRAndSeparateInterpretation(t *testing.T) {
	const raw = "这个倒数在这里说的是切线斜率。"
	const printed = "导数是切线的斜率。"
	const explanation = "结合切线斜率，讲稿中的同音词应指教材里的导数。"
	sources := textbookSources{Teacher: raw, Replay: []textbookEvidence{{Source: "teacher", Text: raw, Timestamp: "00:01:23"}}}
	catalog := makeTextbookEvidence(sources)
	ids := []int{0, 1, 2}
	body := []textbookLine{{ID: 0, Text: "第一章 导数", Width: 45, Height: 12}, {ID: 1, Text: printed, Width: 70, Height: 12},
		{ID: 2, Text: "例：取两点算差商再求极限。", Width: 99, Height: 12}}
	rows, warn, err := textbookDecisionRows(2, body, sources, catalog, []textbookDecision{
		{LineID: &ids[0], textbookSuggestion: textbookSuggestion{Quote: body[0].Text, EvidenceID: &ids[1], Explanation: explanation}},
		{LineID: &ids[1], textbookSuggestion: textbookSuggestion{Quote: printed, EvidenceID: &ids[1], Explanation: explanation}},
		{LineID: &ids[1], textbookSuggestion: textbookSuggestion{Quote: printed, EvidenceID: &ids[1], Evidence: "这是改写的导数解释"}},
		{LineID: &ids[2], textbookSuggestion: textbookSuggestion{Quote: body[2].Text, Source: "ai", Explanation: "先写出两点之间的平均变化率。"}},
	})
	if err != nil || len(rows) != 2 || warn.Evidence != 1 {
		t.Fatalf("rows=%+v warning=%+v err=%v", rows, warn, err)
	}
	first := rows[0]
	if first.Source != "teacher" || first.Quote != printed || len(first.Comments) != 2 ||
		first.Comments[0] != (textbook.Comment{Kind: "teacher", Text: raw, Quote: raw, Timestamp: "00:01:23"}) ||
		first.Comments[1] != (textbook.Comment{Kind: "ai", Text: explanation}) {
		t.Fatalf("attribution mixed: %+v", first)
	}
	if rows[1].Source != "ai" || len(rows[1].Comments) != 1 || rows[1].Comments[0].Quote != "" {
		t.Fatalf("standalone: %+v", rows[1])
	}
}

func TestAnnotationProvenanceForStandaloneAndLocallyCorrectedSources(t *testing.T) {
	inv := &Invoker{}
	_, empty, err := inv.textbookInputSources(context.Background(), nil, nil, "")
	if err != nil || len(makeTextbookEvidence(empty)) != 0 {
		t.Fatalf("standalone: %+v %v", empty, err)
	}
	payload := []byte(`[{"rect":[10,20,100,40],"quote":"变化率","source":"ai","explanation":"比较相邻取值。"}]`)
	rows, _, err := reviewScannedSuggestions(payload, 3, sampleScanPlacement(), empty, nil)
	if err != nil || len(rows) != 1 || len(rows[0].Comments) != 1 || rows[0].Comments[0].Kind != "ai" {
		t.Fatalf("standalone scan: %+v %v", rows, err)
	}
	sources := textbookSources{Teacher: "校订过的本地转写。"}
	id := 1
	row, ok := (textbookSuggestion{Quote: "导数", EvidenceID: &id, Explanation: "适用于可导点。"}).annotation(sources, makeTextbookEvidence(sources))
	if !ok || row.Source == "teacher" || len(row.Comments) != 1 || row.Comments[0].Kind != "ai" {
		t.Fatalf("unproven attribution: %+v", row)
	}
	// Timestamp and teacher content cannot come from the model.
	var suggestion textbookSuggestion
	if err := json.Unmarshal([]byte(`{"quote":"导数","source":"teacher","evidence":"不存在","timestamp":"00:12:00","comments":[{"kind":"teacher","text":"伪造"}]}`), &suggestion); err != nil {
		t.Fatal(err)
	}
	if _, ok := suggestion.annotation(sources, makeTextbookEvidence(sources)); ok {
		t.Fatal("accepted forged quote")
	}
}

func TestTextbookLoadsSavedReplayWithoutLocalLecture(t *testing.T) {
	state := &contract.State{DataRoot: t.TempDir()}
	write, err := storage.OpenWrite(filepath.Join(state.DataRoot, assistantDBFile))
	if err != nil {
		t.Fatal(err)
	}
	defer write.Close()
	if err := storage.BootstrapInto(write); err != nil {
		t.Fatal(err)
	}
	const entryID = "f8c909d1-ccbd-4624-a0b8-52bed3b935a6"
	if err := write.SaveStudyEntries([]map[string]any{{"id": entryID, "weekly": true, "subId": "546"}}); err != nil {
		t.Fatal(err)
	}
	entry, detail, files, err := readTextbookDetail(state, entryID, "课次缺失")
	if err != nil || detail != nil || files != nil || textValue(entry["subId"]) != "546" {
		t.Fatalf("replay-only: entry=%+v detail=%+v files=%+v err=%v", entry, detail, files, err)
	}
}

func TestFetchTextbookSpeechUsesChosenReplayAndReportsLogin(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("sub_id") != "546" || r.Header.Get("Cookie") != "fixture=1" {
			t.Errorf("unexpected replay")
		}
		fmt.Fprint(w, `{"code":0,"list":[{"all_content":[{"BeginSec":83,"EndSec":87,"Text":"原稿里的倒数"},{"Text":"这一条无时间戳"}]}]}`)
	}))
	defer server.Close()
	client := zhiyun.New(server.URL, zhiyun.Session{Cookie: "fixture=1"})
	rows, err := fetchTextbookSpeech(context.Background(), client, "546")
	if err != nil || len(rows) != 2 || rows[0].Timestamp != "00:01:23" || rows[0].Text != "原稿里的倒数" || rows[1].Timestamp != "" {
		t.Fatalf("%+v %v", rows, err)
	}
	client.Session = zhiyun.Session{}
	if _, err := fetchTextbookSpeech(context.Background(), client, "546"); err == nil || !strings.Contains(err.Error(), "登录智云") || calls != 1 {
		t.Fatalf("no session: calls=%d err=%v", calls, err)
	}
}

func TestCachedSpeechRequiresMatchingExportAndOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.txt")
	original := "[00:00:13] 讨论切线斜率。\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	lecture := map[string]any{"id": "local", "transcript_path": path, "transcript_text": original}
	task := &zhiyunTask{LectureID: "local", SubID: "22", State: zhiyunTaskCompleted, OutputPath: path, Kind: "transcript"}
	tasks := []*zhiyunTask{task}
	if matchingTranscriptTask(tasks, lecture, "23") != nil {
		t.Fatal("matched a different replay")
	}
	task = matchingTranscriptTask(tasks, lecture, "22")
	if found := cachedSpeech(task, lecture); len(found) != 1 || found[0].Timestamp != "00:00:13" || found[0].Text != "讨论切线斜率。" {
		t.Fatalf("cached speech: %+v", found)
	}
	lecture["transcript_text"] = "[00:00:13] 本地替换的讲义。\n"
	if cachedSpeech(task, lecture) != nil {
		t.Fatal("accepted replacement text")
	}
}

func TestTeacherIndexLocalReplayRequiresCurrentExport(t *testing.T) {
	state := &contract.State{DataRoot: t.TempDir()}
	db, err := sql.Open("sqlite", filepath.Join(state.DataRoot, assistantDBFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const lectureID = "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(state.DataRoot, "lecture.txt")
	if _, err = db.Exec("CREATE TABLE lectures (id TEXT PRIMARY KEY, transcript_path TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lectures(id,transcript_path) VALUES(?,?)", lectureID, path); err != nil {
		t.Fatal(err)
	}
	inv := &Invoker{}
	store := inv.zhiyunTaskStore(state)
	task, err := store.Create(zhiyunExportInput{Kind: "transcript", CourseID: "12", SubID: "34"}, "第一讲")
	if err != nil {
		t.Fatal(err)
	}
	if got := inv.localTextbookReplay(state, lectureID); got != "" {
		t.Fatalf("accepted unfinished task %q", got)
	}
	if _, err = store.Update(task.ID, func(t *zhiyunTask) { t.State = zhiyunTaskCompleted; t.LectureID = lectureID; t.OutputPath = path }); err != nil {
		t.Fatal(err)
	}
	if got := inv.localTextbookReplay(state, lectureID); got != "34" {
		t.Fatalf("missing matching replay: %q", got)
	}
	if _, err = db.Exec("UPDATE lectures SET transcript_path=? WHERE id=?", path+".new", lectureID); err != nil {
		t.Fatal(err)
	}
	if got := inv.localTextbookReplay(state, lectureID); got != "" {
		t.Fatalf("accepted replaced local transcript %q", got)
	}
}
