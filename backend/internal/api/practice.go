package api

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

var (
	practiceOptionLine = regexp.MustCompile(`^\s*([A-H])[.、．)）:：]\s*(.*)$`)
	answerPrefix       = regexp.MustCompile(`^(?:(?:参考答案|正确答案|答案)\s*[:：]?\s*)?([A-H](?:[、,，\s]*[A-H])*)(?:[.。\s、，,:：]|$)`)
	responsePattern    = regexp.MustCompile(`^[A-H](?:[,，、\s]*[A-H])*$`)
)

func questionOptions(stem string) []string {
	options := []string{}
	current := ""
	for _, line := range strings.Split(stem, "\n") {
		if match := practiceOptionLine.FindStringSubmatch(line); match != nil {
			if current != "" {
				options = append(options, current)
			}
			current = match[1]
			continue
		}
		if current != "" {
			// Option text continues only until the next option line.
			continue
		}
	}
	if current != "" {
		options = append(options, current)
	}
	if len(options) < 2 {
		return []string{}
	}
	seen := map[string]bool{}
	for _, key := range options {
		if seen[key] {
			return []string{}
		}
		seen[key] = true
	}
	return options
}

func answerChoices(answer string, allowed []string) []string {
	match := answerPrefix.FindStringSubmatch(strings.TrimSpace(answer))
	if match == nil {
		return []string{}
	}
	letters := regexp.MustCompile(`[A-H]`).FindAllString(match[1], -1)
	if len(letters) == 0 {
		return []string{}
	}
	allowedSet := map[string]bool{}
	for _, key := range allowed {
		allowedSet[key] = true
	}
	seen := map[string]bool{}
	result := []string{}
	for _, key := range letters {
		if !allowedSet[key] {
			return []string{}
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	for index := 0; index < len(result); index++ {
		for next := index + 1; next < len(result); next++ {
			if result[next] < result[index] {
				result[index], result[next] = result[next], result[index]
			}
		}
	}
	return result
}

func compareChoices(response string, correct []string) bool {
	trimmed := strings.TrimSpace(response)
	if !responsePattern.MatchString(trimmed) {
		return false
	}
	seen := map[string]bool{}
	left := []string{}
	for _, key := range regexp.MustCompile(`[A-H]`).FindAllString(trimmed, -1) {
		if !seen[key] {
			seen[key] = true
			left = append(left, key)
		}
	}
	rightSet := map[string]bool{}
	right := []string{}
	for _, key := range correct {
		if !rightSet[key] {
			rightSet[key] = true
			right = append(right, key)
		}
	}
	if len(left) != len(right) {
		return false
	}
	for _, key := range left {
		if !rightSet[key] {
			return false
		}
	}
	return true
}

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
		return nil, contract.InputInvalid("练习数据无效")
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
	options := questionOptions(textValue(question["stem"]))
	if len(options) == 0 {
		return nil, contract.InputInvalid("非选择题直接查看参考答案，无需作答")
	}
	correct := answerChoices(answer, options)
	correctKeys := make([]any, 0, len(correct))
	for _, key := range correct {
		correctKeys = append(correctKeys, key)
	}
	question["correctOptionKeys"] = correctKeys
	verdict := "review"
	feedback := "参考答案未明确标出选项字母，请对照答案自查。"
	if len(correct) > 0 {
		if compareChoices(input.Response, correct) {
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
		return nil, contract.InputInvalid("练习数据无效")
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
