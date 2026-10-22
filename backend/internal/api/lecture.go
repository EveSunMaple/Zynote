package api

import (
	"encoding/json"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

func handleLectureDetail(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	db, err := openReadOnlyDB(state)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	lecture, err := storage.QueryOne(db, "SELECT * FROM lectures WHERE id=?", input.ID)
	if err != nil {
		return nil, contract.Unavailable("课次读取失败")
	}
	if lecture == nil {
		return nil, contract.NewError(contract.CodeNotFound, "课次不存在")
	}
	checkpoint, err := storage.QueryOne(
		db,
		"SELECT fingerprint,total,segments_json FROM correction_checkpoints WHERE lecture_id=?",
		input.ID,
	)
	if err != nil {
		return nil, contract.Unavailable("校订检查点读取失败")
	}
	subtitles, err := storage.QueryOne(
		db,
		"SELECT srt_text,segments_json FROM subtitles WHERE lecture_id=?",
		input.ID,
	)
	if err != nil {
		return nil, contract.Unavailable("字幕读取失败")
	}
	var segments []any
	srt := ""
	if checkpoint != nil {
		if err := json.Unmarshal([]byte(storage.ToStringValue(checkpoint["segments_json"])), &segments); err != nil {
			return nil, contract.InputInvalid("校订检查点数据无效")
		}
		srt = learning.ToSrtMaps(toSegmentMaps(segments))
	} else if subtitles != nil {
		srt = storage.ToStringValue(subtitles["srt_text"])
		if raw := storage.ToStringValue(subtitles["segments_json"]); raw != "" {
			if err := json.Unmarshal([]byte(raw), &segments); err != nil {
				return nil, contract.InputInvalid("字幕分段数据无效")
			}
		}
	}
	notes := any(nil)
	if row, err := storage.QueryOne(db, "SELECT notes_json FROM lecture_notes WHERE lecture_id=?", input.ID); err != nil {
		return nil, contract.Unavailable("笔记读取失败")
	} else if row != nil {
		if err := json.Unmarshal([]byte(storage.ToStringValue(row["notes_json"])), &notes); err != nil {
			return nil, contract.InputInvalid("笔记数据无效")
		}
	}
	practice := any(nil)
	if row, err := storage.QueryOne(db, "SELECT extended_json FROM practice_sets WHERE lecture_id=?", input.ID); err != nil {
		return nil, contract.Unavailable("练习读取失败")
	} else if row != nil {
		if err := json.Unmarshal([]byte(storage.ToStringValue(row["extended_json"])), &practice); err != nil {
			return nil, contract.InputInvalid("练习数据无效")
		}
	}
	result := map[string]any{
		"lecture":  lecture,
		"segments": segments,
		"srt":      srt,
		"notes":    notes,
		"practice": practice,
	}
	if checkpoint != nil {
		result["correction"] = map[string]any{
			"completed": len(segments),
			"total":     checkpoint["total"],
		}
	}
	return result, nil
}

func toSegmentMaps(values []any) []map[string]any {
	result := []map[string]any{}
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
}
