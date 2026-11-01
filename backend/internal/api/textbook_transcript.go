package api

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
	"github.com/EveSunMaple/Zynote/backend/internal/textbook"
	"github.com/EveSunMaple/Zynote/backend/internal/zhiyun"
)

var exportedSpeechLine = regexp.MustCompile(`^\[(\d{2,}:\d{2}:\d{2})\] (.+)$`)

// The manifest identifies who produced a transcript. A user-supplied local subtitle,
// even one containing timestamps, cannot by itself establish that it came from Zhiyun.
func matchingTranscriptTask(tasks []*zhiyunTask, lecture map[string]any, subID string) *zhiyunTask {
	path := textValue(lecture["transcript_path"])
	if path == "" {
		return nil
	}
	for _, task := range tasks {
		if task.Kind == "transcript" && task.State == zhiyunTaskCompleted && zhiyunDigits.MatchString(task.SubID) &&
			task.LectureID == textValue(lecture["id"]) && task.OutputPath != "" &&
			filepath.Clean(task.OutputPath) == filepath.Clean(path) && (subID == "" || subID == task.SubID) {
			return task
		}
	}
	return nil
}

// Reading saved comments works offline. Only offer a local lecture as a replay source
// if its current transcript still points at the completed Zhiyun export.
func (inv *Invoker) localTextbookReplay(state *contract.State, lectureID string) string {
	if !config.IsUUID(lectureID) {
		return ""
	}
	db, err := openReadOnlyDB(state)
	if err != nil {
		return ""
	}
	defer db.Close()
	lecture, err := storage.QueryOne(db, "SELECT id, transcript_path FROM lectures WHERE id=?", lectureID)
	if err != nil || textValue(lecture["transcript_path"]) == "" {
		return ""
	}
	tasks, err := inv.zhiyunTaskStore(state).List()
	if err != nil {
		return ""
	}
	if task := matchingTranscriptTask(tasks, lecture, ""); task != nil {
		return task.SubID
	}
	return ""
}

func cachedSpeech(task *zhiyunTask, lecture map[string]any) []textbookEvidence {
	if task == nil || !strings.EqualFold(filepath.Ext(task.OutputPath), ".txt") {
		return nil
	}
	original := textValue(lecture["transcript_text"])
	if original == "" || len(original) > 4000000 {
		return nil
	}
	info, err := os.Stat(task.OutputPath)
	if err != nil || info.Size() != int64(len(original)) {
		return nil
	}
	body, err := os.ReadFile(task.OutputPath)
	if err != nil || string(body) != original {
		return nil
	}
	var records []textbookEvidence
	for line := range strings.Lines(original) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		record := textbookEvidence{Source: "teacher", Text: line}
		if match := exportedSpeechLine.FindStringSubmatch(line); match != nil {
			record.Text, record.Timestamp = match[2], match[1]
		}
		records = append(records, record)
	}
	return records
}

func fetchTextbookSpeech(ctx context.Context, client *zhiyun.Client, subID string) ([]textbookEvidence, error) {
	if client == nil || (client.Session.Authorization == "" && client.Session.Cookie == "") {
		return nil, contract.Unavailable("请先登录智云课堂，再读取所选回放中老师讲话的语音转写")
	}
	timed, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	segments, err := client.Transcript(timed, subID)
	if err != nil {
		return nil, contract.Unavailable("无法读取所选智云回放的语音转写：" + err.Error())
	}
	records := make([]textbookEvidence, 0, len(segments))
	for _, segment := range segments {
		stamp := ""
		if !segment.StartUnknown {
			stamp = transcriptClock(float64(segment.Start) * 1000)
		}
		records = append(records, textbookEvidence{Source: "teacher", Text: segment.Text, Timestamp: stamp})
	}
	return records, nil
}

func (inv *Invoker) textbookInputSources(ctx context.Context, store contract.StateStore, state *contract.State, entryID string) (string, textbookSources, error) {
	var sources textbookSources
	if entryID == "" {
		return "", sources, nil
	}
	entry, detail, files, err := readTextbookDetail(state, entryID, "请先保存课表中的课程关联，再生成建议")
	if err != nil {
		return "", sources, err
	}
	chapter := textValue(entry["chapter"])
	lecture, _ := detail["lecture"].(map[string]any)
	subID, err := inv.matchSavedTranscript(state, lecture, textValue(entry["subId"]), &sources)
	if err != nil {
		return "", sources, err
	}
	if err := inv.fillTextbookSources(ctx, store, state, entryID, lecture, files, subID, &sources); err != nil {
		return "", textbookSources{}, err
	}
	if textbookSourcesLength(sources) > 250000 {
		return "", textbookSources{}, contract.InputInvalid("来源资料过长，请先整理本节课对应内容后重试")
	}
	return chapter, sources, nil
}

func (inv *Invoker) matchSavedTranscript(
	state *contract.State,
	lecture map[string]any,
	subID string,
	sources *textbookSources,
) (string, error) {
	if textValue(lecture["transcript_path"]) == "" {
		return subID, nil
	}
	tasks, err := inv.zhiyunTaskStore(state).List()
	if err != nil {
		return "", contract.Unavailable("无法核对智云导出记录，请检查本地数据目录后重试")
	}
	if task := matchingTranscriptTask(tasks, lecture, subID); task != nil {
		sources.Replay = cachedSpeech(task, lecture)
		return task.SubID, nil
	}
	return subID, nil
}

func (inv *Invoker) fillTextbookSources(
	ctx context.Context,
	store contract.StateStore,
	state *contract.State,
	entryID string,
	lecture map[string]any,
	files []map[string]any,
	subID string,
	sources *textbookSources,
) error {
	if subID != "" {
		return inv.resolveReplaySources(ctx, store, lecture, files, subID, sources)
	}
	if lecture == nil {
		return nil
	}
	return inv.fillAttachedSources(ctx, store, state, entryID, sources)
}

func (inv *Invoker) fillAttachedSources(
	ctx context.Context,
	store contract.StateStore,
	state *contract.State,
	entryID string,
	sources *textbookSources,
) error {
	if err := inv.prepareTextbookPPT(ctx, store, state, entryID); err != nil {
		return err
	}
	_, detail, files, err := readTextbookDetail(state, entryID, "请先保存课次关联")
	if err != nil {
		return err
	}
	filled, err := inv.textbookSourcesFor(detail, files)
	if err != nil {
		return err
	}
	*sources = filled
	return nil
}

func (inv *Invoker) resolveReplaySources(
	ctx context.Context,
	store contract.StateStore,
	lecture map[string]any,
	files []map[string]any,
	subID string,
	sources *textbookSources,
) error {
	// A selected replay is the authority. Do not silently substitute a corrected
	// subtitle or another course's transcript if that replay is unavailable.
	if !zhiyunDigits.MatchString(subID) {
		return contract.InputInvalid("智云回放编号无效，请重新选择回放")
	}
	if len(sources.Replay) == 0 {
		client, err := inv.zhiyunClient(store)
		if err != nil {
			return err
		}
		replay, err := fetchTextbookSpeech(ctx, client, subID)
		if err != nil {
			return err
		}
		sources.Replay = replay
	}
	texts := make([]string, 0, len(sources.Replay))
	for _, item := range sources.Replay {
		texts = append(texts, item.Text)
	}
	sources.Teacher = strings.Join(texts, "\n")
	// Keep supporting material when it is already available; a separate slow PPT
	// OCR job must not block analysis based on the requested classroom transcript.
	sources.Senior = textbookSeniorSource(files)
	sources.PPT = textValue(lecture["ppt_text"])
	if sources.PPT != "" {
		ppt, err := inv.textbookPPTSource(lecture)
		if err != nil {
			return err
		}
		sources.PPT = ppt
	}
	return nil
}

func selectTextbookEvidence(catalog []textbookEvidence, id *int) (*textbookEvidence, string) {
	if id == nil || *id <= 0 || *id > len(catalog) {
		return nil, ""
	}
	selected := &catalog[*id-1]
	return selected, selected.Source
}

func (s textbookSuggestion) aiOnly(row *textbook.Highlight, quote, explanation string) bool {
	if explanation == "" || s.Evidence != "" {
		return false
	}
	row.Source, row.Evidence = "ai", quote
	return true
}

func (s textbookSuggestion) sourced(row *textbook.Highlight, sources textbookSources, catalog []textbookEvidence) bool {
	kind := s.Source
	selected, selectedSource := selectTextbookEvidence(catalog, s.EvidenceID)
	if kind == "" {
		kind = selectedSource
	}
	evidence, ok := resolveTextbookEvidence(sources, catalog, kind, s.EvidenceID, s.Evidence)
	if !ok {
		return false
	}
	row.Source, row.Evidence = kind, evidence
	if kind != "teacher" && kind != "focus" {
		return true
	}
	// Old-format replies quote the source directly. Recover a timestamp only
	// if the exact quote belongs to an identified segment of this replay.
	if selected == nil {
		for i := range sources.Replay {
			record := &sources.Replay[i]
			if _, match := evidenceQuote(record.Text, evidence); match {
				selected = record
				break
			}
		}
	}
	if len(sources.Replay) > 0 && selected != nil && selected.Source == "teacher" {
		row.Comments = append(row.Comments, textbook.Comment{Kind: "teacher", Text: evidence, Quote: evidence, Timestamp: selected.Timestamp})
	} else {
		row.Source = "ai"
		row.Evidence = "本地课堂资料：" + evidence
	}
	return true
}

func addExplanation(row *textbook.Highlight, explanation string) {
	if explanation != "" {
		row.Comments = append(row.Comments, textbook.Comment{Kind: "ai", Text: explanation})
	} else if row.Source == "ai" {
		// Older model versions can return a verified local source without a paraphrase.
		row.Comments = append(row.Comments, textbook.Comment{Kind: "ai", Text: row.Evidence})
	}
}

func (s textbookSuggestion) annotation(sources textbookSources, catalog []textbookEvidence) (textbook.Highlight, bool) {
	var row textbook.Highlight
	quote, explanation := strings.TrimSpace(s.Quote), strings.TrimSpace(s.Explanation)
	if quote == "" || utf8.RuneCountInString(quote) > 3000 || utf8.RuneCountInString(explanation) > 1600 {
		return row, false
	}
	row.Quote, row.Extension = quote, s.Extension
	if s.Source == "ai" && s.EvidenceID == nil {
		if !s.aiOnly(&row, quote, explanation) {
			return row, false
		}
	} else if !s.sourced(&row, sources, catalog) {
		return row, false
	}
	addExplanation(&row, explanation)
	return row, true
}
