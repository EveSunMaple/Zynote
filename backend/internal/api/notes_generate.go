package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
)

func transcriptClock(ms float64) string {
	total := int64(ms) / 1000
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total/60)%60, total%60)
}

func dedupeNotesItems(items []any) []any {
	seen := map[string]bool{}
	result := []any{}
	for _, item := range items {
		point, _ := item.(map[string]any)
		if point == nil {
			continue
		}
		key := textValue(point["quote"]) + "\x00" + textValue(point["timestamp"])
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, point)
	}
	return result
}

func (inv *Invoker) handleNotesGenerate(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	detailAny, err := inv.lectureDetailOf(state, input.ID)
	if err != nil {
		return nil, err
	}
	detail := detailAny.(map[string]any)
	if _, exists := detail["correction"]; exists {
		return nil, contract.InputInvalid("字幕仍在校订，请完成剩余批次后生成笔记")
	}
	segmentsRaw, _ := detail["segments"].([]any)
	if len(segmentsRaw) == 0 {
		return nil, contract.InputInvalid("请先生成或导入完整字幕")
	}
	lecture, _ := detail["lecture"].(map[string]any)
	lines := []string{}
	for _, item := range segmentsRaw {
		segment, _ := item.(map[string]any)
		if segment == nil {
			continue
		}
		lines = append(lines, "["+transcriptClock(numberValue(segment["start"]))+"] "+textValue(segment["text"]))
	}
	coursewareText := ""
	limitations := []string{}
	if pptText := textValue(lecture["ppt_text"]); pptText != "" {
		var identity *courseware.SourceIdentity
		if pptPath := textValue(lecture["ppt_path"]); pptPath != "" {
			if current, readErr := courseware.ReadSourceIdentity(pptPath); readErr == nil {
				identity = &current
			}
		}
		parsed := courseware.ParseCacheWithIdentity(pptText, identity)
		coursewareText = parsed.Text
		const marker = "[课件提取限制]"
		if index := strings.LastIndex(coursewareText, marker); index >= 0 {
			for _, line := range strings.Split(coursewareText[index+len(marker):], "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "- ") {
					limitations = append(limitations, strings.TrimSpace(trimmed[2:]))
				}
			}
			coursewareText = strings.TrimSpace(coursewareText[:index])
		}
		limitations = append(limitations, courseware.Limitations(parsed)...)
		if strings.HasPrefix(coursewareText, "[扫描 PDF：") || strings.HasPrefix(coursewareText, "[扫描 PDF:") {
			limitations = append(limitations, "课件为扫描 PDF，未执行 OCR。")
		}
	}
	if len([]rune(coursewareText)) > 12000 {
		coursewareText = string([]rune(coursewareText)[:12000])
		limitations = append(limitations, "用于笔记生成的课件文字超过 12,000 字，仅发送开头部分。")
		inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
			"lectureId": input.ID, "stage": "课件文字受限",
			"message": "笔记请求的课件文字限于前 12,000 字；已将截断情况告知模型",
		}})
	}
	limitations = uniqueStrings(limitations)
	if len(limitations) > 0 {
		inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
			"lectureId": input.ID, "stage": "课件提取限制", "message": strings.Join(limitations, "；"),
		}})
	}
	prompt, err := config.Prompt(state.DataRoot, state.PromptsRoot, "notes")
	if err != nil {
		return nil, err
	}
	if prompt == "" {
		return nil, contract.Unavailable("笔记提示词未配置")
	}
	profile, err := config.ActiveProfile(state.DataRoot, store.Reverse())
	if err != nil {
		return nil, err
	}
	client := ai.New(ai.Profile{
		BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
		APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
	})
	full := strings.Join(lines, "\n")
	parts := courseware.Chunks(full, 16000)
	return inv.runJob("lecture:"+input.ID, func(ctx context.Context) (any, error) {
		result := map[string]any{"keyPoints": []any{}, "notTested": []any{}, "uncertain": []any{}}
		for index, part := range parts {
			message := fmt.Sprintf("提取教师明确表态 %d/%d", index+1, len(parts))
			if len(parts) == 1 {
				message = "结合全课字幕一次提取教师重点、不考与含糊表态"
			}
			inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
				"lectureId": input.ID, "stage": "重点笔记", "message": message,
			}})
			raw, err := client.JSON(ctx, prompt, map[string]any{
				"subtitles": part, "courseware": coursewareText, "coursewareLimitations": limitations,
			}, func(value []byte) error {
				var notes map[string]any
				if err := json.Unmarshal(value, &notes); err != nil {
					return contract.InputInvalid("笔记数据无效")
				}
				return validateNotes(notes)
			})
			if err != nil {
				return nil, err
			}
			var notes map[string]any
			_ = json.Unmarshal(raw, &notes)
			for _, key := range []string{"keyPoints", "notTested", "uncertain"} {
				items, _ := notes[key].([]any)
				existing, _ := result[key].([]any)
				result[key] = append(existing, items...)
			}
		}
		for _, key := range []string{"keyPoints", "notTested", "uncertain"} {
			result[key] = dedupeNotesItems(result[key].([]any))
		}
		segments := notesSegments(detail)
		for _, item := range result["keyPoints"].([]any) {
			verifyNotesPoint(item.(map[string]any), "key", segments)
		}
		for _, item := range result["notTested"].([]any) {
			verifyNotesPoint(item.(map[string]any), "not", segments)
		}
		for _, item := range result["uncertain"].([]any) {
			verifyNotesPoint(item.(map[string]any), "uncertain", segments)
		}
		return inv.persistNotes(state, input.ID, result)
	})
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
