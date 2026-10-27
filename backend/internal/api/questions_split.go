package api

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

const msgQuestionsInvalid = "题目结果无效"

func (inv *Invoker) handleQuestionsSplit(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string `json:"courseId"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("题目资料标识无效")
	}
	db, err := openReadOnlyDB(state)
	if err != nil {
		return nil, err
	}
	file, err := storage.QueryOne(db, "SELECT * FROM course_files WHERE id=? AND course_id=? AND kind='past_exam'", input.ID, input.CourseID)
	db.Close()
	if err != nil {
		return nil, contract.Unavailable("资料读取失败")
	}
	if file == nil {
		return nil, contract.InputInvalid("请选择历年卷资料")
	}
	if truthy(file["needs_ocr"]) {
		return nil, contract.InputInvalid("扫描件需 OCR，当前版本不支持")
	}
	prompt, err := config.Prompt(state.DataRoot, state.PromptsRoot, "past-exam")
	if err != nil {
		return nil, err
	}
	if prompt == "" {
		return nil, contract.Unavailable("拆题提示词未配置")
	}
	profile, err := config.ActiveProfile(state.DataRoot, store.Reverse())
	if err != nil {
		return nil, err
	}
	filename := storage.ToStringValue(file["filename"])
	parts := courseware.Chunks(storage.ToStringValue(file["text"]), 0)
	client := ai.New(ai.Profile{
		BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
		APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
	})
	return inv.runJob("exam:"+input.ID, func(ctx context.Context) (any, error) {
		result := []map[string]any{}
		for index, part := range parts {
			inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
				"stage": "拆题", "message": "正在拆分 " + filename + "（" + itoa(index+1) + "/" + itoa(len(parts)) + "）",
			}})
			raw, err := client.JSON(ctx, prompt, map[string]any{"sourceFile": filename, "text": part}, func(value []byte) error {
				var rows []map[string]any
				if err := json.Unmarshal(value, &rows); err != nil {
					return contract.InputInvalid(msgQuestionsInvalid)
				}
				for _, row := range rows {
					if err := validateSplitQuestion(row); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			var rows []map[string]any
			_ = json.Unmarshal(raw, &rows)
			for _, row := range rows {
				question := map[string]any{
					"type": row["type"], "stem": row["stem"], "knowledgePoints": row["knowledgePoints"],
				}
				if year, ok := row["year"].(string); ok {
					question["year"] = year
				}
				if answer, ok := row["answer"].(string); ok {
					question["answer"] = answer
				}
				question["id"] = ids.New()
				question["courseId"] = input.CourseID
				question["sourceFile"] = filename
				result = append(result, question)
			}
		}
		if len(result) == 0 {
			return nil, contract.InputInvalid("未识别出题目，已保留原有题库")
		}
		write, err := openWriteDB(state)
		if err != nil {
			return nil, err
		}
		defer write.Close()
		if err := write.ReplaceQuestions(input.CourseID, input.ID, result); err != nil {
			return nil, err
		}
		return result, nil
	})
}

func validateSplitQuestion(row map[string]any) error {
	if _, ok := row["type"].(string); !ok || storage.ToStringValue(row["type"]) == "" {
		return contract.InputInvalid(msgQuestionsInvalid)
	}
	if stem, ok := row["stem"].(string); !ok || stem == "" {
		return contract.InputInvalid(msgQuestionsInvalid)
	}
	if year, ok := row["year"]; ok {
		if _, ok := year.(string); !ok {
			return contract.InputInvalid(msgQuestionsInvalid)
		}
	}
	if answer, ok := row["answer"]; ok {
		if _, ok := answer.(string); !ok {
			return contract.InputInvalid(msgQuestionsInvalid)
		}
	}
	points, ok := row["knowledgePoints"].([]any)
	if !ok {
		return contract.InputInvalid(msgQuestionsInvalid)
	}
	for _, point := range points {
		if _, ok := point.(string); !ok {
			return contract.InputInvalid(msgQuestionsInvalid)
		}
	}
	return nil
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		return typed != "" && typed != "0" && typed != "false"
	default:
		return false
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
