package app_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func practiceLLMReply(t *testing.T, input map[string]any) any {
	t.Helper()
	switch {
	case input["selfPractice"] == true:
		rows := []map[string]any{}
		for index := 0; index < 5; index++ {
			rows = append(rows, map[string]any{
				"stem": fmt.Sprintf("自测题 %d 的主要内容是什么", index+1), "type": "简答",
				"topic": "重点", "reason": "字幕依据", "answer": "自测答案", "explanation": "自测解析",
			})
		}
		return rows
	case input["answerLookup"] == true:
		return map[string]any{"sourceUrl": "", "quote": "", "answer": "", "explanation": ""}
	case input["practiceExplanation"] == true:
		return map[string]any{"answer": "AI 答案", "explanation": "AI 详解"}
	case input["pastQuestion"] != nil:
		return []map[string]any{{
			"stem": "拓展题 机器学习的主要目标是什么", "type": "简答", "topic": "重点",
			"reason": "有来源", "sourceUrl": "https://source.example/1", "answer": "特征工程",
		}}
	default:
		return []map[string]any{{"id": "q1", "topic": "重点", "reason": "字幕依据"}}
	}
}

func TestPracticeGenerateAndAnswer(t *testing.T) {
	var llmCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/tavily" {
			fmt.Fprint(writer, `{"results":[{"url":"https://source.example/1","raw_content":"拓展题 机器学习的主要目标是什么 答案：特征工程"}]}`)
			return
		}
		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Messages) < 2 {
			t.Errorf("bad llm request: %v", err)
			return
		}
		var text string
		if err := json.Unmarshal(body.Messages[1].Content, &text); err != nil {
			t.Errorf("bad llm content: %v", err)
			return
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(text), &input); err != nil {
			t.Errorf("bad llm input: %v", err)
			return
		}
		llmCalls++
		payload, _ := json.Marshal(practiceLLMReply(t, input))
		fmt.Fprintf(writer, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, string(payload))
	}))
	defer server.Close()
	t.Setenv("ZYNOTE_TAVILY_URL", server.URL+"/tavily")

	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{"match":"匹配提示词","search":"拓展提示词"},"encryptedSearchKey":"encrypted-search","qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("bootstrap failed: %+v", errReply)
	}
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "练习课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureID := "99999999-9999-4999-8999-999999999999"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'字幕完成')", lectureID, courseID, "第一课"); err != nil {
		t.Fatal(err)
	}
	srt := "1\n00:00:01,000 --> 00:00:02,000\n机器学习的主要目标是什么\n"
	if _, errReply := f.call("subtitles:save", map[string]any{"id": lectureID, "srt": srt}); errReply != nil {
		t.Fatalf("subtitles:save failed: %+v", errReply)
	}
	notes := `{"keyPoints":[{"level":"必考","topic":"重点","detail":"解释","quote":"机器学习的主要目标是什么","timestamp":"00:00:01","verified":true}],"notTested":[],"uncertain":[]}`
	if _, err := db.Exec("INSERT INTO lecture_notes(lecture_id,notes_json) VALUES(?,?)", lectureID, notes); err != nil {
		t.Fatal(err)
	}
	question := `{"id":"q1","courseId":"` + courseID + `","sourceFile":"2024.pdf","type":"单选","stem":"1. 机器学习的主要目标是什么\nA. 特征工程\nB. 数据清洗","answer":"A","knowledgePoints":["重点"]}`
	if _, err := db.Exec("INSERT INTO past_questions(id,course_id,question_json) VALUES(?,?,?)", "q1", courseID, question); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO course_files(id,course_id,kind,filename,path,text,chunks_json,needs_ocr) VALUES(?,?,?,?,?,?,?,0)",
		"file1", courseID, "senior_notes", "笔记.pdf", "/tmp/notes.pdf", "重点 机器学习",
		`["重点 机器学习"]`,
	); err != nil {
		t.Fatal(err)
	}
	practice := `{"past":[{"stem":"1. 题\nA. 甲\nB. 乙","answer":"B","explanation":"解析","type":"单选","topic":"重点","reason":""}],"extended":[],"generated":[],"shortage":0}`
	if _, err := db.Exec("INSERT INTO practice_sets(lecture_id,past_ids_json,extended_json) VALUES(?,?,?)", lectureID, "[]", practice); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, errReply := f.call("practice:answer", map[string]any{"id": lectureID, "group": "past", "index": 0})
	if errReply != nil {
		t.Fatalf("practice:answer failed: %+v", errReply)
	}
	var answered map[string]any
	_ = json.Unmarshal(raw, &answered)
	first := answered["past"].([]any)[0].(map[string]any)
	if first["answerSource"] != "imported" {
		t.Fatalf("answer source mismatch: %s", raw)
	}
	keys, _ := first["correctOptionKeys"].([]any)
	if len(keys) != 1 || keys[0] != "B" {
		t.Fatalf("correct keys mismatch: %s", raw)
	}
	before := llmCalls
	raw, errReply = f.call("practice:generate", map[string]any{"id": lectureID, "withSearch": true})
	if errReply != nil {
		t.Fatalf("practice:generate failed: %+v", errReply)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	past := result["past"].([]any)
	if len(past) != 1 || past[0].(map[string]any)["topic"] != "重点" {
		t.Fatalf("past mismatch: %s", raw)
	}
	if len(result["generated"].([]any)) != 5 {
		t.Fatalf("generated mismatch: %s", raw)
	}
	pastQuestion := past[0].(map[string]any)
	if pastQuestion["answerSource"] != "imported" || pastQuestion["explanation"] != "AI 详解" {
		t.Fatalf("prepared past mismatch: %s", raw)
	}
	extended := result["extended"].([]any)
	if len(extended) != 1 || extended[0].(map[string]any)["sourceUrl"] != "https://source.example/1" ||
		extended[0].(map[string]any)["answer"] != "特征工程" || result["shortage"] != float64(0) {
		t.Fatalf("extended mismatch: %s", raw)
	}
	if llmCalls <= before {
		t.Fatal("llm was not called")
	}
	raw, errReply = f.call("lecture:detail", map[string]any{"id": lectureID})
	if errReply != nil {
		t.Fatal(errReply)
	}
	var detail map[string]any
	_ = json.Unmarshal(raw, &detail)
	if detail["practice"] == nil {
		t.Fatalf("practice not persisted: %s", raw)
	}
	if strings.Contains(string(raw), "自测解析") == false {
		t.Fatalf("generated explanation missing: %s", raw)
	}
}
