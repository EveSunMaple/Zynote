package api

import (
	"encoding/json"
	"math"
	"os"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

func (inv *Invoker) handleCourses(state *contract.State) (any, error) {
	db, err := openReadOnlyDB(state)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	courses, err := storage.QueryAll(db, "SELECT * FROM courses ORDER BY created_at,id")
	if err != nil {
		return nil, contract.Unavailable("课程读取失败")
	}
	return courses, nil
}

func (inv *Invoker) handleImagePick(store contract.StateStore, state *contract.State, kind string) (any, error) {
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	title := "选择聊天背景"
	if kind == "avatar" {
		title = "选择头像"
	}
	raw, err := reverse.Call("shell.pickFile", map[string]any{
		"title": title, "extensions": []string{"png", "jpg", "jpeg", "webp"},
	}, 600000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Path *string `json:"path"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("文件选择器响应无效")
	}
	if picked.Path == nil || *picked.Path == "" {
		return nil, nil
	}
	info, err := os.Lstat(*picked.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, contract.InputInvalid("图片文件无效")
	}
	if kind == "avatar" && info.Size() > 5*1024*1024 {
		return nil, contract.InputInvalid("请选择小于 5 MB 的头像图片")
	}
	if kind == "background" && info.Size() > 16*1024*1024 {
		return nil, contract.InputInvalid("请选择小于 16 MB 的背景图片")
	}
	mode := "background"
	if kind == "avatar" {
		mode = "avatar"
	}
	encoded, err := reverse.Call("shell.imageDataUrl", map[string]any{
		"path": *picked.Path, "mode": mode,
	}, 30000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var value struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(encoded, &value) != nil || value.Value == "" {
		return nil, contract.Unavailable("图片处理失败")
	}
	return value.Value, nil
}

func (inv *Invoker) handleZhiyunControl(store contract.StateStore, action string, params json.RawMessage) (any, error) {
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("智云窗口不可用")
	}
	switch action {
	case "zhiyun:login", "zhiyun:refresh", "zhiyun:back":
		method := "zhiyun." + strings.TrimPrefix(action, "zhiyun:")
		if _, err := reverse.Call(method, nil, 15000); err != nil {
			return nil, contract.Unavailable(err.Error())
		}
		return true, nil
	case "zhiyun:bounds":
		var input struct {
			Bounds  map[string]float64 `json:"bounds"`
			Visible bool               `json:"visible"`
		}
		if err := json.Unmarshal(params, &input); err != nil || input.Bounds == nil {
			return nil, contract.InputInvalid("智云窗口参数无效")
		}
		for _, key := range []string{"x", "y", "width", "height"} {
			value, ok := input.Bounds[key]
			if !ok || value < 0 || value > 10000 || math.IsNaN(value) {
				return nil, contract.InputInvalid("智云窗口参数无效")
			}
		}
		if _, err := reverse.Call("zhiyun.bounds", map[string]any{
			"bounds": input.Bounds, "visible": input.Visible,
		}, 15000); err != nil {
			return nil, contract.Unavailable(err.Error())
		}
		return true, nil
	}
	return nil, contract.InputInvalid("未知操作")
}
