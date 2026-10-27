package app_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type correctionCall struct {
	Model  string
	Rows   []int
	Images int
	Labels []string
}

type correctionServer struct {
	mu       sync.Mutex
	calls    []correctionCall
	failText string
}

func (server *correctionServer) callsSnapshot() []correctionCall {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]correctionCall{}, server.calls...)
}

func correctionInput(content json.RawMessage) (string, []string, error) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text, nil, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return "", nil, err
	}
	input := ""
	labels := []string{}
	for _, part := range parts {
		switch part.Type {
		case "text":
			if strings.Contains(part.Text, `"rows"`) {
				input = part.Text
			} else {
				labels = append(labels, part.Text)
			}
		case "image_url":
			if part.ImageURL == nil {
				return "", nil, fmt.Errorf("missing image url")
			}
		}
	}
	if index := strings.Index(input, "\n上次输出"); index >= 0 {
		input = input[:index]
	}
	return input, labels, nil
}

func (server *correctionServer) handler(t *testing.T) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if len(body.Messages) < 2 {
			t.Errorf("messages missing")
			return
		}
		input, labels, err := correctionInput(body.Messages[1].Content)
		if err != nil {
			t.Errorf("input: %v", err)
			return
		}
		var parsed struct {
			Rows []struct {
				ID   int    `json:"id"`
				Text string `json:"text"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(input), &parsed); err != nil {
			t.Errorf("rows: %v", err)
			return
		}
		call := correctionCall{Model: body.Model, Images: len(labels), Labels: labels}
		for _, row := range parsed.Rows {
			call.Rows = append(call.Rows, row.ID)
		}
		server.mu.Lock()
		server.calls = append(server.calls, call)
		failText := server.failText
		server.mu.Unlock()
		for _, row := range parsed.Rows {
			if failText != "" && strings.Contains(row.Text, failText) {
				fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"not json"}}]}`)
				return
			}
		}
		rows := make([]map[string]any, 0, len(parsed.Rows))
		for _, row := range parsed.Rows {
			uncertain := row.ID == 1
			rows = append(rows, map[string]any{
				"id": row.ID, "text": row.Text + "（校订）", "uncertain": uncertain,
				"reason": map[bool]string{true: "术语依据", false: ""}[uncertain],
			})
		}
		payload, err := json.Marshal(rows)
		if err != nil {
			t.Errorf("payload: %v", err)
			return
		}
		fmt.Fprintf(writer, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, string(payload))
	}
}

func buildCourseware(t *testing.T, withImage bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	files := map[string]string{
		"ppt/presentation.xml":            `<p:presentation><p:sldIdLst><p:sldId id="1" r:id="r1"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships><Relationship Id="r1" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":           `<p:sld><a:t>数据清洗 缺失值 特征工程</a:t></p:sld>`,
	}
	if withImage {
		files["ppt/slides/slide1.xml"] = `<p:sld><a:t>数据清洗 缺失值 特征工程</a:t><a:blip r:embed="rId1"/></p:sld>`
		files["ppt/slides/_rels/slide1.xml.rels"] = `<Relationships><Relationship Id="rId1" Target="../media/image1.png"/></Relationships>`
		files["ppt/media/image1.png"] = "fake-png-bytes"
	} else {
		files["ppt/slides/_rels/slide1.xml.rels"] = `<Relationships/>`
	}
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type subtitleFixture struct {
	core        *fixture
	courseID    string
	lectureID   string
	dataRoot    string
	libraryRoot string
}

func startSubtitleFixture(t *testing.T, serverURL string, withImage bool) subtitleFixture {
	t.Helper()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{"correction":"校订提示词"},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+serverURL+`/v1","model":"text-test","visionModel":"vision-test","apiFormat":"chat_completions","encryptedKey":"encrypted","parameters":{"text-test":{"maxTokens":250},"vision-test":{"maxTokens":250}}}}`)
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "字幕课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	transcript := filepath.Join(t.TempDir(), "原文.srt")
	if err := os.WriteFile(transcript, []byte("1\n00:00:01,000 --> 00:00:02,000\n第一句话\n\n2\n00:00:02,000 --> 00:00:03,000\n第二句话\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ppt := filepath.Join(t.TempDir(), "课件.pptx")
	if err := os.WriteFile(ppt, buildCourseware(t, withImage), 0o644); err != nil {
		t.Fatal(err)
	}
	lectureRaw, errReply := f.call("lecture:create", map[string]any{
		"courseId": courseID, "title": "第一课", "transcriptPath": transcript, "pptPath": ppt,
	})
	if errReply != nil {
		t.Fatalf("lecture:create failed: %+v", errReply)
	}
	var lecture map[string]any
	_ = json.Unmarshal(lectureRaw, &lecture)
	return subtitleFixture{
		core: f, courseID: courseID, lectureID: lecture["id"].(string),
		dataRoot: dataRoot, libraryRoot: libraryRoot,
	}
}

func TestSubtitlesGenerateText(t *testing.T) {
	fake := &correctionServer{}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	f := startSubtitleFixture(t, server.URL, false)
	raw, errReply := f.core.call("subtitles:generate", map[string]any{"id": f.lectureID, "mode": "text"})
	if errReply != nil {
		t.Fatalf("subtitles:generate failed: %+v", errReply)
	}
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	lecture := detail["lecture"].(map[string]any)
	if lecture["status"] != "字幕完成" {
		t.Fatalf("status mismatch: %v", lecture["status"])
	}
	segments := detail["segments"].([]any)
	if len(segments) != 2 {
		t.Fatalf("segments mismatch: %s", raw)
	}
	first := segments[0].(map[string]any)
	second := segments[1].(map[string]any)
	if first["text"] != "第一句话（校订）" || first["originalText"] != "第一句话" ||
		second["uncertain"] != true || second["reason"] != "术语依据" {
		t.Fatalf("segment fields mismatch: %s", raw)
	}
	if _, exists := detail["correction"]; exists {
		t.Fatalf("checkpoint should be cleared: %s", raw)
	}
	srt := detail["srt"].(string)
	if !strings.Contains(srt, "第一句话（校订）") || !strings.Contains(srt, "第二句话（校订）") {
		t.Fatalf("srt mismatch: %s", srt)
	}
	path := filepath.Join(f.dataRoot, "courses", f.courseID, "transcript-results", f.lectureID, "校订字幕.srt")
	if body, err := os.ReadFile(path); err != nil || string(body) != srt {
		t.Fatalf("srt file mismatch: %v %q", err, body)
	}
	calls := fake.callsSnapshot()
	if len(calls) != 2 || calls[0].Model != "text-test" || calls[0].Images != 0 {
		t.Fatalf("call mismatch: %+v", calls)
	}
}

func TestSubtitlesGenerateVisionAndResume(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	fake := &correctionServer{failText: "第二句话"}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	f := startSubtitleFixture(t, server.URL, true)
	_, errReply := f.core.call("subtitles:generate", map[string]any{"id": f.lectureID, "mode": "quality"})
	if errReply == nil || !strings.Contains(errReply.Message, "已保存 1/2 条") {
		t.Fatalf("expected resumable failure: %+v", errReply)
	}
	raw, _ := f.core.call("lecture:detail", map[string]any{"id": f.lectureID})
	var detail map[string]any
	_ = json.Unmarshal(raw, &detail)
	lecture := detail["lecture"].(map[string]any)
	if lecture["status"] != "校订未完成，可继续" {
		t.Fatalf("failure status mismatch: %v", lecture["status"])
	}
	correction, _ := detail["correction"].(map[string]any)
	if correction == nil || correction["completed"] != float64(1) {
		t.Fatalf("checkpoint mismatch: %s", raw)
	}
	fake.mu.Lock()
	fake.failText = ""
	fake.mu.Unlock()
	before := len(fake.callsSnapshot())
	raw, errReply = f.core.call("subtitles:generate", map[string]any{"id": f.lectureID, "mode": "quality"})
	if errReply != nil {
		t.Fatalf("resume failed: %+v", errReply)
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail["segments"].([]any)) != 2 {
		t.Fatalf("resume segments mismatch: %s", raw)
	}
	calls := fake.callsSnapshot()
	if len(calls) <= before || len(calls)-before != 1 {
		t.Fatalf("resume should only request the remaining batch: %+v", calls)
	}
	for _, call := range calls {
		if call.Model != "vision-test" {
			t.Fatalf("vision model mismatch: %+v", call)
		}
	}
	foundImage := false
	for _, call := range calls {
		if call.Images == 1 && len(call.Labels) > 0 && strings.Contains(call.Labels[0], "课件第 1 页") {
			foundImage = true
		}
	}
	if !foundImage {
		t.Fatalf("image attachment missing: %+v", calls)
	}
}

func TestSubtitlesGenerateRequiresSources(t *testing.T) {
	fake := &correctionServer{}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	f := startSubtitleFixture(t, server.URL, false)
	db, err := sql.Open("sqlite", filepath.Join(f.dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM lectures WHERE id=?", f.lectureID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'待校订')", f.lectureID, f.courseID, "空课次"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, errReply := f.core.call("subtitles:generate", map[string]any{"id": f.lectureID}); errReply == nil ||
		!strings.Contains(errReply.Message, "语音识别原文") {
		t.Fatalf("missing transcript mismatch: %+v", errReply)
	}
	raw, _ := f.core.call("lecture:detail", map[string]any{"id": f.lectureID})
	var detail map[string]any
	_ = json.Unmarshal(raw, &detail)
	lecture := detail["lecture"].(map[string]any)
	if lecture["status"] != "需核对" {
		t.Fatalf("error status mismatch: %v", lecture["status"])
	}
}
