package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/zhiyun"
)

const msgZhiyunLoginUnavailable = "智云登录状态不可用"

var zhiyunDigits = regexp.MustCompile(`^\d+$`)

func (inv *Invoker) zhiyunClient(store contract.StateStore) (*zhiyun.Client, error) {
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable(msgZhiyunLoginUnavailable)
	}
	raw, err := reverse.Call("shell.zhiyunSession", map[string]any{}, 5000)
	if err != nil {
		return nil, contract.Unavailable(msgZhiyunLoginUnavailable)
	}
	var session struct {
		Cookie        string `json:"cookie"`
		Authorization string `json:"authorization"`
	}
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, contract.Unavailable(msgZhiyunLoginUnavailable)
	}
	return zhiyun.New("", zhiyun.Session{Cookie: session.Cookie, Authorization: session.Authorization}), nil
}

func (inv *Invoker) handleZhiyunSearch(store contract.StateStore, _ *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Keyword string `json:"keyword"`
		Page    int    `json:"page"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("搜索关键词无效")
	}
	input.Keyword = strings.TrimSpace(input.Keyword)
	if len([]rune(input.Keyword)) < 1 || len([]rune(input.Keyword)) > 100 {
		return nil, contract.InputInvalid("搜索关键词无效")
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Page < 1 || input.Page > 1000 {
		return nil, contract.InputInvalid("页码无效")
	}
	client, err := inv.zhiyunClient(store)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	result, err := client.Search(ctx, input.Keyword, input.Page)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleZhiyunLessons(store contract.StateStore, _ *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !zhiyunDigits.MatchString(input.CourseID) {
		return nil, contract.InputInvalid("智云课程标识无效")
	}
	client, err := inv.zhiyunClient(store)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	lessons, err := client.Lessons(ctx, input.CourseID)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	return lessons, nil
}
