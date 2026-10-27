package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

const msgPracticeInvalid = "练习数据无效"

func practiceGroup(practice map[string]any, group string, index int) (map[string]any, error) {
	items, _ := practice[group].([]any)
	if index < 0 || index >= len(items) {
		return nil, contract.InputInvalid("题目不存在")
	}
	question, _ := items[index].(map[string]any)
	if question == nil {
		return nil, contract.InputInvalid("题目不存在")
	}
	return question, nil
}

func practiceMap(value any) (map[string]any, error) {
	practice, ok := value.(map[string]any)
	if !ok || practice == nil {
		return nil, contract.InputInvalid("还没有练习")
	}
	return practice, nil
}

func (inv *Invoker) handlePracticeSubmit(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID       string `json:"id"`
		Group    string `json:"group"`
		Index    int    `json:"index"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) ||
		(input.Group != "past" && input.Group != "extended" && input.Group != "generated") {
		return nil, contract.InputInvalid(msgPracticeInvalid)
	}
	input.Response = strings.TrimSpace(input.Response)
	if len([]rune(input.Response)) < 1 || len([]rune(input.Response)) > 10000 {
		return nil, contract.InputInvalid("答案内容无效")
	}
	detail, err := inv.lectureDetailOf(state, input.ID)
	if err != nil {
		return nil, err
	}
	practice, err := practiceMap(detail.(map[string]any)["practice"])
	if err != nil {
		return nil, err
	}
	question, err := practiceGroup(practice, input.Group, input.Index)
	if err != nil {
		return nil, err
	}
	answer := textValue(question["answer"])
	if answer == "" {
		return nil, contract.InputInvalid("请先补全已有题目的答案，再作答")
	}
	options := learning.QuestionOptions(textValue(question["stem"]))
	if len(options) == 0 {
		return nil, contract.InputInvalid("非选择题直接查看参考答案，无需作答")
	}
	correct := learning.AnswerChoices(answer, options)
	correctKeys := make([]any, 0, len(correct))
	for _, key := range correct {
		correctKeys = append(correctKeys, key)
	}
	question["correctOptionKeys"] = correctKeys
	verdict := "review"
	feedback := "参考答案未明确标出选项字母，请对照答案自查。"
	if len(correct) > 0 {
		if learning.CompareChoices(input.Response, correct) {
			verdict = "correct"
			feedback = "选项正确。"
		} else {
			verdict = "incorrect"
			feedback = "所选选项与参考答案不一致。"
		}
	}
	question["attempt"] = map[string]any{
		"response": input.Response, "verdict": verdict, "feedback": feedback,
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if err := write.SavePractice(input.ID, practice); err != nil {
		return nil, err
	}
	return practice, nil
}

func (inv *Invoker) handlePracticeReset(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID    string `json:"id"`
		Group string `json:"group"`
		Index int    `json:"index"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) ||
		(input.Group != "past" && input.Group != "extended" && input.Group != "generated") {
		return nil, contract.InputInvalid(msgPracticeInvalid)
	}
	detail, err := inv.lectureDetailOf(state, input.ID)
	if err != nil {
		return nil, err
	}
	practice, err := practiceMap(detail.(map[string]any)["practice"])
	if err != nil {
		return nil, err
	}
	question, err := practiceGroup(practice, input.Group, input.Index)
	if err != nil {
		return nil, err
	}
	delete(question, "attempt")
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if err := write.SavePractice(input.ID, practice); err != nil {
		return nil, err
	}
	return practice, nil
}

func (inv *Invoker) handlePracticeAnswer(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID    string `json:"id"`
		Group string `json:"group"`
		Index int    `json:"index"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) || input.Index < 0 ||
		(input.Group != "past" && input.Group != "extended" && input.Group != "generated") {
		return nil, contract.InputInvalid(msgPracticeInvalid)
	}
	return inv.runJob("lecture:"+input.ID, func(ctx context.Context) (any, error) {
		detail, err := inv.lectureDetailOf(state, input.ID)
		if err != nil {
			return nil, err
		}
		detailMap := detail.(map[string]any)
		practice, err := practiceMap(detailMap["practice"])
		if err != nil {
			return nil, err
		}
		question, err := practiceGroup(practice, input.Group, input.Index)
		if err != nil {
			return nil, err
		}
		lecture, _ := detailMap["lecture"].(map[string]any)
		course, err := inv.courseName(state, textValue(lecture["course_id"]))
		if err != nil {
			return nil, err
		}
		dependencies, _ := inv.practiceDependencies(state, store)
		prepared, err := learning.PrepareAnswer(ctx, question, course, dependencies)
		if err != nil {
			return nil, err
		}
		items, _ := practice[input.Group].([]any)
		items[input.Index] = prepared
		if err := savePracticeSet(state, input.ID, practice); err != nil {
			return nil, err
		}
		return practice, nil
	})
}

func (inv *Invoker) handlePracticePrepare(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	return inv.runJob("lecture:"+input.ID, func(ctx context.Context) (any, error) {
		detail, err := inv.lectureDetailOf(state, input.ID)
		if err != nil {
			return nil, err
		}
		detailMap := detail.(map[string]any)
		practice, err := practiceMap(detailMap["practice"])
		if err != nil {
			return nil, err
		}
		lecture, _ := detailMap["lecture"].(map[string]any)
		course, err := inv.courseName(state, textValue(lecture["course_id"]))
		if err != nil {
			return nil, err
		}
		dependencies, _ := inv.practiceDependencies(state, store)
		for _, group := range []string{"past", "extended", "generated"} {
			items, _ := practice[group].([]any)
			for index, raw := range items {
				question, _ := raw.(map[string]any)
				if question == nil {
					continue
				}
				prepared, err := learning.PrepareAnswer(ctx, question, course, dependencies)
				if err != nil {
					return nil, err
				}
				items[index] = prepared
			}
		}
		if err := savePracticeSet(state, input.ID, practice); err != nil {
			return nil, err
		}
		return practice, nil
	})
}
