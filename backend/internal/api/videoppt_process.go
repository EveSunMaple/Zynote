package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

const defaultVideoPrompt = "可以帮我把这个视频里面的ppt截取出来吗，我要求你仅仅截绿色方框部分（没有绿色方框时自行确认 PPT 的位置），尽量去截取没有笔记的第一时间的ppt，然后最后删除重复，检查无误后保存成pdf然后把其它之前用于生成这个pdf的文件（包括视频）删了。最终 PDF 或其他成果保存成功、可以打开且逐页检查通过后，按本次任务的具体文件清单清理下载视频、分片、截帧图片、中间 PPT 和临时导出，不能按扩展名删除整个目录。保留正式成果、教材、原有有效资料及备份、数据库、配置和登录数据。未完成核验或失败时保留恢复所需的文件和记录，核验完成后清理；清理失败必须说明，不能声称已删除。"

func videoProcessor(state *contract.State) videoppt.Processor {
	return videoppt.Processor{
		Root: filepath.Join(state.LibraryRoot, "视频提取临时任务"), Resources: state.ResourcesRoot,
		Platform:            media.CurrentMediaPlatform(),
		AllowSystemFallback: !state.Packaged || os.Getenv("ZYNOTE_SYSTEM_MEDIA") == "1",
	}
}

func (inv *Invoker) videoChat(store contract.StateStore, state *contract.State) (videoppt.ChatFunc, error) {
	profile, err := config.ActiveProfile(state.DataRoot, store.Reverse())
	if err != nil {
		return nil, err
	}
	client := ai.New(ai.Profile{
		BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
		APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
	})
	return func(ctx context.Context, system, text string, images []videoppt.Image) (string, error) {
		parts := []ai.ContentPart{{Type: "text", Text: text}}
		for _, image := range images {
			parts = append(parts, ai.ContentPart{Type: "text", Text: image.Label})
			parts = append(parts, ai.ContentPart{Type: "image_url", ImageURL: &ai.ImageURL{URL: image.URL}})
		}
		return client.Chat(ctx, []ai.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: parts},
		}, nil)
	}, nil
}

func (inv *Invoker) handleVideoPptSource(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(params, &input); err != nil || (input.Path == "" && input.URL == "") {
		return nil, contract.InputInvalid("视频来源无效")
	}
	if input.Path == "" {
		return nil, contract.InputInvalid("当前版本请先选择本地视频")
	}
	return inv.runJob("video-ppt", func(ctx context.Context) (any, error) {
		chat, err := inv.videoChat(store, state)
		if err != nil {
			return nil, err
		}
		task, err := videoProcessor(state).CreateTask(input.Path)
		if err != nil {
			return nil, videoPptError(err)
		}
		prepared, err := videoProcessor(state).Prepare(ctx, task, chat, inv.videoProgress)
		if err != nil {
			return nil, videoPptError(err)
		}
		return prepared.Source, nil
	})
}

func (inv *Invoker) handleVideoPptExtract(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID     string          `json:"id"`
		Crop   videoppt.Region `json:"crop"`
		Prompt string          `json:"prompt"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("视频任务参数无效")
	}
	prompt := input.Prompt
	if prompt == "" {
		prompt = defaultVideoPrompt
	}
	return inv.runJob("video-ppt", func(ctx context.Context) (any, error) {
		task, err := videoPptService(state).VideoTask(input.ID)
		if err != nil {
			return nil, videoPptError(err)
		}
		chat, err := inv.videoChat(store, state)
		if err != nil {
			return nil, err
		}
		review, err := videoProcessor(state).Extract(ctx, task, input.Crop, prompt, chat, inv.videoProgress)
		if err != nil {
			return nil, videoPptError(err)
		}
		return review, nil
	})
}
