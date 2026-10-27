package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

var exportFormats = map[string][]string{
	"subtitles":       {"srt", "txt"},
	"notes":           {"md", "docx"},
	"practice":        {"md", "docx", "pdf"},
	"evidence-bundle": {"json", "md"},
}

func notesExportMarkdown(detail map[string]any, title string) (string, error) {
	notes, _ := detail["notes"].(map[string]any)
	if notes == nil {
		return "", contract.InputInvalid("还没有听课笔记")
	}
	out := []string{"# " + title + " · 听课笔记", ""}
	groups := []struct {
		Title string
		Key   string
	}{{"教师明确重点", "keyPoints"}, {"明确不考", "notTested"}, {"表态含糊", "uncertain"}}
	for _, group := range groups {
		out = append(out, "## "+group.Title, "")
		items, _ := notes[group.Key].([]any)
		for _, raw := range items {
			point, _ := raw.(map[string]any)
			if point == nil {
				continue
			}
			out = append(out, notesPointSection(point)...)
		}
	}
	return strings.Join(out, "\n"), nil
}

func notesPointSection(point map[string]any) []string {
	heading := textValue(point["topic"])
	if level := textValue(point["level"]); level != "" {
		heading = "[" + level + "] " + heading
	}
	out := []string{
		"### " + heading,
		textValue(point["timestamp"]) + " · " + map[bool]string{true: "原话已核验", false: "⚠ 疑似编造或时间不符"}[truthy(point["verified"])],
		"", "> " + textValue(point["quote"]), "",
	}
	if detailText := textValue(point["detail"]); detailText != "" {
		out = append(out, detailText)
	}
	if pptRef := textValue(point["pptRef"]); pptRef != "" {
		out = append(out, "课件："+pptRef)
	}
	return append(out, "")
}

func practiceQuestionSection(index int, question map[string]any) []string {
	out := []string{
		fmt.Sprintf("### %d. %s · %s", index+1, textValue(question["topic"]), textValue(question["type"])), "",
		textValue(question["stem"]), "",
	}
	source := textValue(question["sourceUrl"])
	if source == "" {
		source = textValue(question["sourceFile"])
	}
	line := "来源：" + source
	if year := textValue(question["year"]); year != "" {
		line += " · " + year
	}
	out = append(out, line, "关联："+textValue(question["reason"]), "")
	answer := textValue(question["answer"])
	if answer != "" {
		label := "参考答案"
		if truthy(question["aiAnswer"]) {
			label = "AI 生成，仅供参考"
		}
		out = append(out, label+":\n"+answer)
	} else {
		out = append(out, "暂无答案")
	}
	if url := textValue(question["answerSourceUrl"]); url != "" {
		out = append(out, "答案来源："+url)
	}
	if explanation := textValue(question["explanation"]); explanation != "" {
		out = append(out, "详解（AI 辅助整理）：\n"+explanation)
	}
	if attempt, _ := question["attempt"].(map[string]any); attempt != nil {
		verdict := map[string]string{"correct": "正确", "incorrect": "需要再练习", "review": "待自查"}[textValue(attempt["verdict"])]
		out = append(out, "我的回答："+textValue(attempt["response"])+"\n判定："+verdict+"\n"+textValue(attempt["feedback"]))
	}
	return append(out, "")
}

func practiceExportMarkdown(detail map[string]any, title string) (string, error) {
	practice, _ := detail["practice"].(map[string]any)
	if practice == nil {
		return "", contract.InputInvalid("还没有练习题")
	}
	out := []string{"# " + title + " · 课后练习", ""}
	groups := []struct {
		Title string
		Key   string
	}{{"历年真题", "past"}, {"有来源的拓展练习", "extended"}, {"AI 自测题（非历年真题）", "generated"}}
	for _, group := range groups {
		items, _ := practice[group.Key].([]any)
		out = append(out, fmt.Sprintf("## %s（%d 道）", group.Title, len(items)), "")
		for index, raw := range items {
			question, _ := raw.(map[string]any)
			if question == nil {
				continue
			}
			out = append(out, practiceQuestionSection(index, question)...)
		}
	}
	if shortage := int(numberValue(practice["shortage"])); shortage > 0 {
		out = append(out, fmt.Sprintf("拓展题不足 %d 道，未用 AI 编题冒充搜索结果。", shortage))
	}
	return strings.Join(out, "\n"), nil
}

func exportMarkdown(detail map[string]any, kind string) (string, error) {
	lecture, _ := detail["lecture"].(map[string]any)
	title := textValue(lecture["title"])
	if kind == "notes" {
		return notesExportMarkdown(detail, title)
	}
	return practiceExportMarkdown(detail, title)
}

func exportInput(params json.RawMessage) (string, string, string, error) {
	var input struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return "", "", "", contract.InputInvalid("课次标识无效")
	}
	formats, ok := exportFormats[input.Kind]
	if !ok {
		return "", "", "", contract.InputInvalid("不支持的导出格式") // NOSONAR: repeated literal documents the protocol field name at each use.
	}
	for _, format := range formats {
		if format == input.Format {
			return input.ID, input.Kind, input.Format, nil
		}
	}
	return "", "", "", contract.InputInvalid("不支持的导出格式")
}

func (inv *Invoker) exportEvidence(state *contract.State, detail map[string]any, format string) ([]byte, error) {
	lecture, _ := detail["lecture"].(map[string]any)
	courseID := textValue(lecture["course_id"])
	course, err := inv.courseRecord(state, courseID)
	if err != nil {
		return nil, err
	}
	bundle := buildEvidenceBundle(course, detail, time.Now().UTC().Format(time.RFC3339Nano), state.AppVersion)
	if format == "md" {
		return []byte(evidenceMarkdown(bundle)), nil
	}
	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, contract.Unavailable("证据包生成失败")
	}
	return encoded, nil
}

func exportSubtitles(detail map[string]any, format string) ([]byte, error) {
	segments, _ := detail["segments"].([]any)
	if len(segments) == 0 {
		return nil, contract.InputInvalid("没有字幕可导出")
	}
	if format == "srt" {
		return []byte(textValue(detail["srt"])), nil
	}
	lines := []string{}
	for _, raw := range segments {
		segment, _ := raw.(map[string]any)
		if segment != nil {
			lines = append(lines, "["+subtitleClock(int64(numberValue(segment["start"])), false)+"] "+textValue(segment["text"]))
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func exportTextDocument(store contract.StateStore, detail map[string]any, kind, format, path string) ([]byte, error) {
	text, err := exportMarkdown(detail, kind)
	if err != nil {
		return nil, err
	}
	switch format {
	case "md":
		return []byte(text), nil
	case "docx":
		data, err := notesDocx(text)
		if err != nil {
			return nil, contract.Unavailable("笔记导出失败")
		}
		return data, nil
	case "pdf":
		html := `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><style>body{font:12pt 'Microsoft YaHei',sans-serif;line-height:1.6;padding:24px}pre{white-space:pre-wrap;word-break:break-word;font:inherit}</style><pre>` + escapeHTML(text) + `</pre>`
		reverse := store.Reverse()
		if reverse == nil {
			return nil, contract.Unavailable("PDF 导出组件不可用")
		}
		if _, err := reverse.Call("shell.printHtmlPdf", map[string]any{"html": html, "path": path}, 120000); err != nil {
			return nil, contract.Unavailable("笔记导出失败")
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.HasPrefix(data, []byte("%PDF-")) {
			return nil, contract.Unavailable("笔记导出校验失败")
		}
		return data, nil
	}
	return nil, contract.InputInvalid("不支持的导出格式")
}

func (inv *Invoker) handleExport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	id, kind, format, err := exportInput(params)
	if err != nil {
		return nil, err
	}
	detailAny, err := inv.lectureDetailOf(state, id)
	if err != nil {
		return nil, err
	}
	detail := detailAny.(map[string]any)
	lecture, _ := detail["lecture"].(map[string]any)
	courseName, err := inv.courseName(state, textValue(lecture["course_id"]))
	if err != nil {
		return nil, err
	}
	category := "Notes"
	if kind == "practice" {
		category = "PastExams"
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, category, courseName)
	if err != nil {
		return nil, err
	}
	filename := library.FolderName(textValue(lecture["title"])) + "-" + kind + "." + format
	path, err := library.ReserveFile(directory, filename)
	if err != nil {
		return nil, err
	}
	var data []byte
	switch kind {
	case "evidence-bundle":
		data, err = inv.exportEvidence(state, detail, format)
	case "subtitles":
		data, err = exportSubtitles(detail, format)
	default:
		data, err = exportTextDocument(store, detail, kind, format, path)
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if format != "pdf" {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			_ = os.Remove(path)
			return nil, contract.Unavailable("导出失败")
		}
	}
	check, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(check, data) {
		_ = os.Remove(path)
		return nil, contract.Unavailable("导出校验失败")
	}
	return path, nil
}
