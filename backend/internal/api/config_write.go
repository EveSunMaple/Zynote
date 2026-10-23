package api

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func ensureCourseExists(state *contract.State, id string) error {
	db, err := openReadOnlyDB(state)
	if err != nil {
		return err
	}
	defer db.Close()
	var exists int
	if err := db.QueryRow("SELECT 1 FROM courses WHERE id=?", id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contract.NewError(contract.CodeNotFound, "课程不存在")
		}
		return contract.Unavailable("数据库读取失败")
	}
	return nil
}

func handleAppearanceSave(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Value    json.RawMessage `json:"value"`
		CourseID *string         `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || len(input.Value) == 0 {
		return nil, contract.InputInvalid("外观参数无效")
	}
	if input.CourseID != nil {
		if err := ensureCourseExists(state, *input.CourseID); err != nil {
			return nil, err
		}
	}
	return config.SaveAppearance(state.DataRoot, input.Value, input.CourseID)
}

func handleAppearanceClear(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.CourseID == "" {
		return nil, contract.InputInvalid("外观课程标识无效")
	}
	if err := ensureCourseExists(state, input.CourseID); err != nil {
		return nil, err
	}
	return config.ClearAppearance(state.DataRoot, input.CourseID)
}

func handleIdentitySave(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Identity json.RawMessage `json:"identity"`
	}
	if err := json.Unmarshal(params, &input); err != nil || len(input.Identity) == 0 {
		return nil, contract.InputInvalid("身份参数无效")
	}
	return config.SaveIdentity(state.DataRoot, input.Identity)
}
