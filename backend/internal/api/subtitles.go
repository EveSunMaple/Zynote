package api

import (
	"encoding/json"
	"os"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

func (inv *Invoker) saveSubtitles(state *contract.State, lectureID, srt string) (any, error) {
	segments, err := documents.ParseSrt(srt)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	if err := write.SaveSubtitles(lectureID, srt, segments); err != nil {
		write.Close()
		return nil, err
	}
	write.Close()
	return inv.lectureDetailOf(state, lectureID)
}

func (inv *Invoker) handleSubtitlesSave(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID  string `json:"id"`
		SRT string `json:"srt"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	return inv.saveSubtitles(state, input.ID, input.SRT)
}

func (inv *Invoker) handleSubtitlesImport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	raw, err := reverse.Call("shell.pickFile", map[string]any{
		"title": "选择字幕文件", "extensions": []string{"srt"},
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
		return inv.lectureDetailOf(state, input.ID)
	}
	info, err := os.Lstat(*picked.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16*1024*1024 {
		return nil, contract.InputInvalid("字幕文件无效")
	}
	body, err := os.ReadFile(*picked.Path)
	if err != nil {
		return nil, contract.InputInvalid("字幕文件不可读")
	}
	return inv.saveSubtitles(state, input.ID, string(body))
}
