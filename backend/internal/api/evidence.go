package api

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

const msgTeacherAmbiguous = "教师表态含糊"

var evidencePageMarker = regexp.MustCompile(`\[第\s*(\d+)\s*页\]`)

func evidenceHash(value any) string {
	encoded, _ := json.Marshal(value)
	return sha256Hex(string(encoded))
}

func evidenceMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func evidenceList(value any) []any {
	result, _ := value.([]any)
	return result
}

func evidenceBlock(status, note string, data any, contentHash string) map[string]any {
	block := map[string]any{"status": status, "note": note}
	if contentHash != "" {
		block["contentHash"] = contentHash
	}
	if data != nil {
		block["data"] = data
	}
	return block
}

func evidenceFilename(value string) string {
	if value == "" {
		return ""
	}
	return filepath.Base(strings.ReplaceAll(value, "\\", "/"))
}

func evidenceStatusForNotes(items []any, present bool) string {
	if !present || len(items) == 0 {
		return "unavailable"
	}
	for _, raw := range items {
		item := evidenceMap(raw)
		if verified, ok := item["verified"].(bool); !ok || !verified {
			return "uncertain"
		}
	}
	return "verified"
}

func evidenceNoteEntry(item map[string]any, category string) map[string]any {
	status := "uncertain"
	if category != msgTeacherAmbiguous {
		if verified, ok := item["verified"].(bool); ok && verified {
			status = "verified"
		}
	}
	entry := map[string]any{
		"status": status, "quoteVerified": item["verified"] == true, "category": category,
		"topic": textValue(item["topic"]), "quote": textValue(item["quote"]), "timestamp": textValue(item["timestamp"]),
	}
	for _, key := range []string{"detail", "level", "pptRef"} {
		if value := textValue(item[key]); value != "" {
			entry[key] = value
		}
	}
	return entry
}

func evidenceNoteItems(value any, present bool, category string) map[string]any {
	items := evidenceList(value)
	data := []any{}
	for _, raw := range items {
		data = append(data, evidenceNoteEntry(evidenceMap(raw), category))
	}
	status := evidenceStatusForNotes(items, present)
	if category == msgTeacherAmbiguous && len(data) > 0 {
		status = "uncertain"
	}
	contentHash := ""
	if len(data) > 0 {
		contentHash = evidenceHash(data)
	}
	return evidenceBlock(status, "verified 仅表示应用的原话/时间匹配校验通过；含糊陈述仍按其类别保留。", data, contentHash)
}

func evidencePracticeSource(item map[string]any) map[string]any {
	source := map[string]any{}
	if url := textValue(item["sourceUrl"]); url != "" {
		source["url"] = url
	}
	if file := textValue(item["sourceFile"]); file != "" {
		source["file"] = file
	}
	if year := textValue(item["year"]); year != "" {
		source["year"] = year
	}
	return source
}

func evidencePracticeEntry(item map[string]any, aiGenerated bool) map[string]any {
	answer := textValue(item["answer"])
	entry := map[string]any{
		"status": "uncertain", "aiGenerated": aiGenerated || item["aiAnswer"] == true,
		"topic": textValue(item["topic"]), "type": textValue(item["type"]), "stem": textValue(item["stem"]),
		"reason": textValue(item["reason"]), "source": evidencePracticeSource(item),
		"answerStatus": map[bool]string{true: "uncertain", false: "unavailable"}[answer != ""],
	}
	if answer != "" {
		entry["answer"] = answer
	}
	if answerSource := textValue(item["answerSource"]); answerSource != "" {
		entry["answerSource"] = answerSource
	} else if item["aiAnswer"] == true {
		entry["answerSource"] = "ai"
	}
	for _, key := range []string{"answerSourceUrl", "explanation"} {
		if value := textValue(item[key]); value != "" {
			entry[key] = value
		}
	}
	if attempt := item["attempt"]; attempt != nil {
		entry["attempt"] = attempt
	}
	return entry
}

func evidencePracticeItems(value any, present, aiGenerated bool) map[string]any {
	items := evidenceList(value)
	data := []any{}
	for _, raw := range items {
		data = append(data, evidencePracticeEntry(evidenceMap(raw), aiGenerated))
	}
	status := "unavailable"
	if present {
		status = "uncertain"
		if len(data) == 0 {
			status = "unavailable"
		}
	}
	note := "当前课次没有已保存的练习题集。"
	if present {
		note = "练习集已保存，但当前没有题目条目。"
		if len(data) > 0 {
			note = "题目来源、答案和应用内作答记录照原值导出；此包不对题目或参考答案作外部真实性背书。"
		}
	}
	contentHash := ""
	if len(data) > 0 {
		contentHash = evidenceHash(data)
	}
	return evidenceBlock(status, note, data, contentHash)
}

func evidencePageMarkers(text string) []int {
	markers := []int{}
	for _, match := range evidencePageMarker.FindAllStringSubmatch(text, -1) {
		page := 0
		fmt.Sscanf(match[1], "%d", &page)
		if page > 0 {
			markers = append(markers, page)
		}
	}
	return markers
}

func evidenceSegmentsTraceable(segments []any, complete bool) bool {
	if !complete {
		return false
	}
	for _, raw := range segments {
		segment := evidenceMap(raw)
		if textValue(segment["originalText"]) == "" || segment["uncertain"] != false {
			return false
		}
	}
	return true
}

func evidenceSubtitleData(segments []any, lecture map[string]any, partial bool) []any {
	data := []any{}
	for _, raw := range segments {
		segment := evidenceMap(raw)
		start, end := int64(numberValue(segment["start"])), int64(numberValue(segment["end"]))
		id := evidenceHash([]any{textValue(lecture["id"]), start, end, textValue(segment["text"])})
		if len(id) > 24 {
			id = id[:24]
		}
		entry := map[string]any{
			"id": id, "startMs": start, "endMs": end, "text": textValue(segment["text"]),
			"status": "verified",
		}
		if original := textValue(segment["originalText"]); original != "" {
			entry["originalText"] = original
		}
		uncertain, hasUncertain := segment["uncertain"].(bool)
		reason := textValue(segment["reason"])
		if (hasUncertain && uncertain) || reason != "" {
			value := map[string]any{"flagged": hasUncertain && uncertain}
			if reason != "" {
				value["reason"] = reason
			}
			entry["uncertainty"] = value
		}
		if partial || !hasUncertain || uncertain || textValue(segment["originalText"]) == "" {
			entry["status"] = "uncertain"
		}
		data = append(data, entry)
	}
	return data
}

func evidenceSubtitleState(segments []any, partial, allTraceable bool) (status, note, evidenceStatus, hash string) {
	status = "unavailable"
	note = "字幕来源方式或原文对照未完整保存，无法据此确认每条字幕的校订来源。"
	if len(segments) == 0 {
		return
	}
	status = "uncertain"
	if partial {
		note = "字幕校订仍在进行；覆盖字段记录已保存片段数和总片段数，不能代表全课完整。"
	} else if allTraceable {
		status = "verified"
		note = "每条字幕都有原始文本对照且没有待核对标记；这只描述应用中的校订状态。"
	}
	evidenceStatus = status
	for _, raw := range segments {
		if evidenceMap(raw)["uncertain"] == true {
			evidenceStatus = "uncertain"
		}
	}
	hash = evidenceHash(segments)
	return
}

func evidenceTranscriptState(available bool, text string) (status, note, hash string) {
	if !available {
		return "unavailable", "数据库中没有保存的语音原文文本。", ""
	}
	return "uncertain", "导出的是数据库中保存的语音识别原文；哈希针对保存的文本，不含本地文件字节。", sha256Hex(text)
}

func evidenceCoursewareState(available bool, lecture map[string]any, text string) (status, note, hash string) {
	if available {
		return "uncertain", "包含已保存的提取文本哈希；课件文件本身不打包，且原有记录未保存逐页 OCR/置信度。", sha256Hex(text)
	}
	if textValue(lecture["ppt_path"]) == "" {
		return "unavailable", "没有关联课件。", ""
	}
	return "unavailable", "已关联课件文件，但数据库没有可导出的提取文本。", ""
}

func evidencePracticeData(practice map[string]any, present bool) (map[string]any, string) {
	data := map[string]any{
		"past":      evidencePracticeItems(practice["past"], present, false),
		"extended":  evidencePracticeItems(practice["extended"], present, false),
		"generated": evidencePracticeItems(practice["generated"], present, true),
		"shortage":  numberValue(practice["shortage"]),
	}
	if !present {
		return data, ""
	}
	return data, evidenceHash(practice)
}

func evidenceNotesState(notes map[string]any, present bool, blocks ...map[string]any) (status, hash string) {
	if !present {
		return "unavailable", ""
	}
	status = "verified"
	empty := true
	for _, block := range blocks {
		if textValue(block["status"]) == "uncertain" {
			status = "uncertain"
		}
		if len(block["data"].([]any)) > 0 {
			empty = false
		}
	}
	if empty {
		status = "unavailable"
	}
	return status, evidenceHash(notes)
}

func evidenceNoteReferences(notes map[string]any) []any {
	refs := []any{}
	for _, raw := range evidenceList(notes["keyPoints"]) {
		point := evidenceMap(raw)
		if ref := textValue(point["pptRef"]); ref != "" {
			refs = append(refs, map[string]any{
				"topic": textValue(point["topic"]), "timestamp": textValue(point["timestamp"]), "locator": ref, "status": "uncertain",
			})
		}
	}
	return refs
}

func evidenceCoverage(segments []any, partial bool, correction map[string]any) map[string]any {
	coverage := map[string]any{"segmentCount": len(segments), "partial": partial}
	if len(segments) > 0 {
		start := int64(numberValue(evidenceMap(segments[0])["start"]))
		end := int64(numberValue(evidenceMap(segments[0])["end"]))
		for _, raw := range segments {
			segment := evidenceMap(raw)
			if value := int64(numberValue(segment["start"])); value < start {
				start = value
			}
			if value := int64(numberValue(segment["end"])); value > end {
				end = value
			}
		}
		coverage["startMs"] = start
		coverage["endMs"] = end
	}
	if partial {
		coverage["completed"] = numberValue(correction["completed"])
		coverage["total"] = numberValue(correction["total"])
	}
	return coverage
}

func buildEvidenceBundle(course map[string]any, detail map[string]any, generatedAt, version string) map[string]any {
	lecture := evidenceMap(detail["lecture"])
	segments := evidenceList(detail["segments"])
	correction := evidenceMap(detail["correction"])
	partial := correction != nil
	notes := evidenceMap(detail["notes"])
	notesPresent := detail["notes"] != nil
	practice := evidenceMap(detail["practice"])
	practicePresent := detail["practice"] != nil
	transcriptText := textValue(lecture["transcript_text"])
	coursewareText := textValue(lecture["ppt_text"])
	transcriptAvailable := transcriptText != ""
	coursewareAvailable := coursewareText != ""
	pageMarkers := evidencePageMarkers(coursewareText)
	subtitlesComplete := len(segments) > 0 && !partial
	allTraceable := evidenceSegmentsTraceable(segments, subtitlesComplete)
	notesKeyPoints := evidenceNoteItems(notes["keyPoints"], notes["keyPoints"] != nil, "教师明确重点")
	notesNotTested := evidenceNoteItems(notes["notTested"], notes["notTested"] != nil, "教师明确不考")
	notesUncertain := evidenceNoteItems(notes["uncertain"], notes["uncertain"] != nil, msgTeacherAmbiguous)
	subtitleData := evidenceSubtitleData(segments, lecture, partial)
	subtitleStatus, subtitleNote, subtitleEvidenceStatus, subtitleHash := evidenceSubtitleState(segments, partial, allTraceable)
	transcriptStatus, transcriptNote, transcriptHash := evidenceTranscriptState(transcriptAvailable, transcriptText)
	coursewareStatus, coursewareNote, coursewareHash := evidenceCoursewareState(coursewareAvailable, lecture, coursewareText)
	practiceData, practiceHash := evidencePracticeData(practice, practicePresent)
	notesStatus, noteHash := evidenceNotesState(notes, notesPresent, notesKeyPoints, notesNotTested, notesUncertain)
	noteReferences := evidenceNoteReferences(notes)
	coverage := evidenceCoverage(segments, partial, correction)
	revisionId := evidenceHash(map[string]any{
		"course":         []any{course["id"], course["name"], course["created_at"]},
		"lecture":        []any{lecture["id"], lecture["title"], lecture["created_at"], lecture["status"]},
		"files":          []any{filed(lecture, "transcript_path"), filed(lecture, "ppt_path"), filed(lecture, "source_video_path")},
		"transcriptHash": nullString(transcriptHash),
		"subtitleHash":   nullString(subtitleHash),
		"coursewareHash": nullString(coursewareHash),
		"noteHash":       nullString(noteHash),
		"practiceHash":   nullString(practiceHash),
	})
	return map[string]any{
		"schemaVersion": "1.0.0", "generatedAt": generatedAt, "revisionId": revisionId,
		"producer":          map[string]any{"name": "摘云", "version": nullString(version)},
		"verificationScope": "verified 只表示本地应用已有的原话匹配或来源校订标记通过；不表示模型内容已由外部权威、教师或原始媒体独立证实。",
		"course":            map[string]any{"id": textValue(course["id"]), "name": textValue(course["name"]), "createdAt": textValue(course["created_at"])},
		"lecture":           map[string]any{"id": textValue(lecture["id"]), "title": textValue(lecture["title"]), "createdAt": textValue(lecture["created_at"]), "status": textValue(lecture["status"]), "coverage": coverage},
		"sources": map[string]any{
			"transcript": evidenceBlock(transcriptStatus, transcriptNote, transcriptData(lecture, transcriptText, transcriptAvailable), transcriptHash),
			"subtitles":  evidenceBlock(subtitleStatus, subtitleNote, map[string]any{"segmentCount": len(segments), "complete": subtitlesComplete}, subtitleHash),
			"courseware": evidenceBlock(coursewareStatus, coursewareNote, coursewareData(lecture, coursewareAvailable, pageMarkers), coursewareHash),
			"video":      videoData(lecture),
		},
		"evidence": map[string]any{
			"subtitles": evidenceBlock(subtitleEvidenceStatus, map[bool]string{true: "当前为部分校订快照，必须结合 coverage.completed/total 阅读。", false: "verified 表示存在原文对照且应用未标记待核对，不代表独立人工听审。"}[partial], subtitleData, subtitleHash),
			"notes": evidenceBlock(notesStatus, map[bool]string{true: "各类别保留独立状态及原句时间；verified 仅表示应用内匹配校验通过。", false: "当前课次没有已保存的课程笔记。"}[notesPresent], map[string]any{
				"keyPoints": notesKeyPoints, "notTested": notesNotTested, "uncertain": notesUncertain,
			}, noteHash),
			"summary":       evidenceBlock(textValue(notesKeyPoints["status"]), "摘要要点直接复用本节已保存的教师明确重点笔记，不另行生成视频摘要；每条保留原句时间与应用内核验状态。", notesKeyPoints["data"], textValue(notesKeyPoints["contentHash"])),
			"courseware":    evidenceBlock(coursewareStatus, map[bool]string{true: "提取文本可用，但不是逐页 OCR 核验记录；只有存储文本中已有页码标记才输出页索引。", false: "未保存可导出的课件文字。"}[coursewareAvailable], evidenceCoursewareData(coursewareText, coursewareAvailable, pageMarkers, noteReferences), coursewareHash),
			"frames":        evidenceBlock("unavailable", "当前课程数据模型没有与课次关联的画面帧/哈希索引；未生成画面证据。", []any{}, ""),
			"knowledgeBase": evidenceBlock("unavailable", "当前课次数据没有持久化的知识库引用记录；未生成知识库引用。", []any{}, ""),
			"practice":      evidenceBlock(map[bool]string{true: "uncertain", false: "unavailable"}[practicePresent && (len(evidenceList(practice["past"]))+len(evidenceList(practice["extended"]))+len(evidenceList(practice["generated"])) > 0)], map[bool]string{true: "作答结果是应用保存的本地判定记录，不等同于教师评分；题目和答案真实性未经本导出流程独立确认。", false: "当前课次没有已保存的练习题集。"}[practicePresent], practiceData, practiceHash),
		},
	}
}

func transcriptData(lecture map[string]any, text string, available bool) map[string]any {
	data := map[string]any{"textAvailable": available}
	if name := evidenceFilename(textValue(lecture["transcript_path"])); name != "" {
		data["filename"] = name
	}
	if available {
		data["text"] = text
	}
	return data
}

func coursewareData(lecture map[string]any, available bool, pages []int) map[string]any {
	data := map[string]any{"textAvailable": available, "pageMarkers": pages}
	if name := evidenceFilename(textValue(lecture["ppt_path"])); name != "" {
		data["filename"] = name
	}
	return data
}

func videoData(lecture map[string]any) map[string]any {
	path := textValue(lecture["source_video_path"])
	if path == "" {
		path = textValue(lecture["video_path"])
	}
	status := "unavailable"
	data := map[string]any{"framesAvailable": false}
	if path != "" {
		status = "uncertain"
		data["filename"] = evidenceFilename(path)
	}
	return evidenceBlock(status, "只记录数据库中是否关联视频文件；课次数据模型未保存对应的画面识别索引。", data, "")
}

func evidenceCoursewareData(text string, available bool, pages []int, references []any) map[string]any {
	data := map[string]any{
		"pageIndex": pageIndex(pages), "noteReferences": references,
		"ocr": evidenceBlock("unavailable", "当前课次数据没有可关联的 OCR 结果或置信度记录。", []any{}, ""),
	}
	if available {
		data["extractedText"] = text
	}
	return data
}

func filed(lecture map[string]any, key string) any {
	value := evidenceFilename(textValue(lecture[key]))
	if value == "" {
		return nil
	}
	return value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func pageIndex(pages []int) []any {
	result := []any{}
	for _, page := range pages {
		result = append(result, map[string]any{"page": page, "locator": fmt.Sprintf("[第 %d 页]", page)})
	}
	return result
}

var evidenceStatusText = map[string]string{
	"verified": "已核验（应用内标记）", "uncertain": "待核对/来源不完整", "unavailable": "不可用",
}

func appendEvidenceSection(lines []string, title string, block map[string]any) []string {
	lines = append(lines, "## "+title+" · "+evidenceStatusText[textValue(block["status"])], "", textValue(block["note"]))
	if hash := textValue(block["contentHash"]); hash != "" {
		lines = append(lines, "", "内容 SHA-256："+hash)
	}
	return append(lines, "")
}

func evidenceTranscriptLines(lines []string, source map[string]any) []string {
	data := evidenceMap(source["data"])
	if data == nil {
		return lines
	}
	if name := textValue(data["filename"]); name != "" {
		lines = append(lines, "存储文件："+name, "")
	}
	if text := textValue(data["text"]); text != "" {
		lines = append(lines, "### 原始语音识别文本", "", "```text", text, "```", "")
	}
	return lines
}

func evidenceVideoLines(lines []string, source map[string]any) []string {
	data := evidenceMap(source["data"])
	if data == nil {
		return lines
	}
	if name := textValue(data["filename"]); name != "" {
		lines = append(lines, "存储文件："+name, "")
	}
	return lines
}

func evidenceSubtitleLines(lines []string, block map[string]any) []string {
	for _, raw := range evidenceList(block["data"]) {
		segment := evidenceMap(raw)
		lines = append(lines, "### "+fmt.Sprintf("%d–%d ms", int64(numberValue(segment["startMs"])), int64(numberValue(segment["endMs"])))+" · "+evidenceStatusText[textValue(segment["status"])], "", "片段 ID："+textValue(segment["id"]), "", textValue(segment["text"]), "")
		if original := textValue(segment["originalText"]); original != "" {
			lines = append(lines, "原文对照："+original, "")
		}
		if uncertainty := evidenceMap(segment["uncertainty"]); uncertainty != nil {
			if reason := textValue(uncertainty["reason"]); reason != "" {
				lines = append(lines, "待核对原因："+reason, "")
			}
		}
	}
	return lines
}

func evidenceNotesLines(lines []string, block map[string]any) []string {
	notesData := evidenceMap(block["data"])
	titles := map[string]string{"keyPoints": "教师明确重点", "notTested": "教师明确不考", "uncertain": msgTeacherAmbiguous}
	for _, key := range []string{"keyPoints", "notTested", "uncertain"} {
		if notesData == nil {
			continue
		}
		note := evidenceMap(notesData[key])
		lines = appendEvidenceSection(lines, titles[key], note)
		for _, raw := range evidenceList(note["data"]) {
			item := evidenceMap(raw)
			line := "- [" + evidenceStatusText[textValue(item["status"])] + "] " + textValue(item["timestamp"]) + " · " + textValue(item["topic"]) + "：“" + textValue(item["quote"]) + "”"
			if level := textValue(item["level"]); level != "" {
				line += "（" + level + "）"
			}
			if ref := textValue(item["pptRef"]); ref != "" {
				line += "；课件引用：" + ref
			}
			if detail := textValue(item["detail"]); detail != "" {
				line += "\n  " + detail
			}
			lines = append(lines, line)
		}
		lines = append(lines, "")
	}
	return lines
}

func evidenceSummaryLines(lines []string, block map[string]any) []string {
	for _, raw := range evidenceList(block["data"]) {
		item := evidenceMap(raw)
		lines = append(lines, "- ["+evidenceStatusText[textValue(item["status"])]+"] "+textValue(item["timestamp"])+" · "+textValue(item["topic"])+"：“"+textValue(item["quote"])+"”")
	}
	return append(lines, "")
}

func evidenceCoursewareLines(lines []string, block map[string]any) []string {
	data := evidenceMap(block["data"])
	if data == nil {
		return lines
	}
	if extracted := textValue(data["extractedText"]); extracted != "" {
		lines = append(lines, "```text", extracted, "```", "")
	}
	for _, raw := range evidenceList(data["pageIndex"]) {
		page := evidenceMap(raw)
		lines = append(lines, fmt.Sprintf("- 第 %d 页 · %s", int(numberValue(page["page"])), textValue(page["locator"])))
	}
	for _, raw := range evidenceList(data["noteReferences"]) {
		ref := evidenceMap(raw)
		lines = append(lines, "- 笔记引用："+textValue(ref["topic"])+" · "+textValue(ref["timestamp"])+" · "+textValue(ref["locator"]))
	}
	return append(lines, "")
}

func evidencePracticeLines(lines []string, block map[string]any) []string {
	data := evidenceMap(block["data"])
	if data == nil {
		return lines
	}
	for _, group := range []struct{ Key, Title string }{{"past", "历年真题"}, {"extended", "有来源的拓展练习"}, {"generated", "AI 自测题（非历年真题）"}} {
		groupData := evidenceMap(data[group.Key])
		lines = append(lines, "### "+group.Title+"（"+fmt.Sprintf("%d", len(evidenceList(groupData["data"])))+" 道）", "")
		for _, raw := range evidenceList(groupData["data"]) {
			item := evidenceMap(raw)
			lines = append(lines, fmt.Sprintf("- %s · %s：%s", textValue(item["topic"]), textValue(item["type"]), textValue(item["stem"])))
		}
		lines = append(lines, "")
	}
	if shortage := int(numberValue(data["shortage"])); shortage > 0 {
		lines = append(lines, fmt.Sprintf("拓展题不足 %d 道，未用 AI 编题冒充搜索结果。", shortage))
	}
	return lines
}

func evidenceMarkdown(bundle map[string]any) string {
	course := evidenceMap(bundle["course"])
	lecture := evidenceMap(bundle["lecture"])
	lines := []string{
		"# " + textValue(course["name"]) + " · " + textValue(lecture["title"]) + " · 课程学习证据包",
		"", "- Schema：" + textValue(bundle["schemaVersion"]),
		"- 生成应用：摘云 " + textValue(evidenceMap(bundle["producer"])["version"]),
		"- 生成时间：" + textValue(bundle["generatedAt"]),
		"- 课程 ID：" + textValue(course["id"]) + "；创建：" + textValue(course["createdAt"]),
		"- 课次 ID：" + textValue(lecture["id"]) + "；创建：" + textValue(lecture["createdAt"]) + "；当前状态：" + textValue(lecture["status"]),
		"- 内容修订指纹：" + textValue(bundle["revisionId"]),
		"- 核验范围：" + textValue(bundle["verificationScope"]),
		"- 字幕覆盖：" + fmt.Sprintf("%d", int(numberValue(evidenceMap(lecture["coverage"])["segmentCount"]))) + " 条",
		"",
	}
	sources := evidenceMap(bundle["sources"])
	evidence := evidenceMap(bundle["evidence"])
	lines = appendEvidenceSection(lines, "来源：语音原文", evidenceMap(sources["transcript"]))
	lines = evidenceTranscriptLines(lines, evidenceMap(sources["transcript"]))
	lines = appendEvidenceSection(lines, "来源：字幕", evidenceMap(sources["subtitles"]))
	lines = appendEvidenceSection(lines, "来源：视频文件", evidenceMap(sources["video"]))
	lines = evidenceVideoLines(lines, evidenceMap(sources["video"]))
	lines = appendEvidenceSection(lines, "字幕片段", evidenceMap(evidence["subtitles"]))
	lines = evidenceSubtitleLines(lines, evidenceMap(evidence["subtitles"]))
	lines = appendEvidenceSection(lines, "课程笔记", evidenceMap(evidence["notes"]))
	lines = evidenceNotesLines(lines, evidenceMap(evidence["notes"]))
	lines = appendEvidenceSection(lines, "摘要要点（复用教师明确重点）", evidenceMap(evidence["summary"]))
	lines = evidenceSummaryLines(lines, evidenceMap(evidence["summary"]))
	lines = appendEvidenceSection(lines, "来源：课件/提取文本", evidenceMap(sources["courseware"]))
	lines = appendEvidenceSection(lines, "课件文本与页索引", evidenceMap(evidence["courseware"]))
	lines = evidenceCoursewareLines(lines, evidenceMap(evidence["courseware"]))
	lines = appendEvidenceSection(lines, "画面证据", evidenceMap(evidence["frames"]))
	lines = appendEvidenceSection(lines, "知识库引用", evidenceMap(evidence["knowledgeBase"]))
	lines = appendEvidenceSection(lines, "练习与作答", evidenceMap(evidence["practice"]))
	lines = evidencePracticeLines(lines, evidenceMap(evidence["practice"]))
	return strings.Join(lines, "\n") + "\n"
}

func (inv *Invoker) courseRecord(state *contract.State, courseID string) (map[string]any, error) {
	db, err := openReadOnlyDB(state)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	row, err := storage.QueryOne(db, "SELECT id,name,created_at FROM courses WHERE id=?", courseID)
	if err != nil {
		return nil, contract.Unavailable("课程读取失败")
	}
	if row == nil {
		return nil, contract.NewError(contract.CodeNotFound, "课程不存在")
	}
	return row, nil
}
