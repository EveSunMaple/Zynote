package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	mediaBilibili "github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlesync"
)

func subtitleSyncService(state *contract.State) *subtitlesync.Service {
	return subtitlesync.New(filepath.Join(state.LibraryRoot, "字幕同步记录"), state.LibraryRoot)
}

func (inv *Invoker) subtitleSyncDependencies(store contract.StateStore, state *contract.State, courseName string) subtitlesync.Dependencies {
	knowledgeStore, _ := inv.knowledgeStoreFor(state)
	return subtitlesync.Dependencies{
		Acquire: func(ctx context.Context, item subtitlesync.Item) (string, string, error) {
			subtitle, err := mediaBilibili.FetchVideoSubtitles(ctx, nil, item.URL)
			if err != nil {
				return "", "", err
			}
			path, err := saveVideoSubtitleFile(state, item.URL, item.Title, subtitle.SRT, courseName)
			if err != nil {
				return "", "", err
			}
			return subtitle.SRT, path, nil
		},
		Publish: func(ctx context.Context, item subtitlesync.Item, srt, category string) (bool, string, error) {
			if knowledgeStore == nil {
				return false, "", contract.Unavailable("知识库不可用")
			}
			sum := sha256.Sum256([]byte(srt))
			sourceMethod := item.SourceMethod
			input := knowledge.ImportInput{
				Title: "P" + itoa(item.Page) + " · " + item.Title, Text: srt, Locator: item.URL,
				SourceHash: hex.EncodeToString(sum[:]), Category: category, Origin: "bilibili-subtitle",
			}
			if sourceMethod != "" {
				input.SourceMethod = &sourceMethod
			}
			if item.Generation != nil && *item.Generation != "" {
				raw := json.RawMessage(`"` + *item.Generation + `"`)
				input.ExpectedGeneration = &raw
			}
			result, err := knowledgeStore.Import(input)
			if err != nil {
				return false, "", err
			}
			unchanged, _ := result["unchanged"].(bool)
			generation := ""
			if entry, ok := result["entry"].(map[string]any); ok {
				generation = textValue(entry["generation_id"])
			}
			return unchanged, generation, nil
		},
		Generation: func(ctx context.Context, url string) (string, error) {
			if knowledgeStore == nil {
				return "", nil
			}
			value, err := knowledgeStore.Generation(url)
			if err != nil || value == nil {
				return "", nil
			}
			if text, ok := value.(string); ok {
				return text, nil
			}
			return "", nil
		},
		Progress: inv.videoProgress,
	}
}

func (inv *Invoker) handleSubtitleSyncList(state *contract.State) (any, error) {
	tasks, err := subtitleSyncService(state).List()
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	return tasks, nil
}

func (inv *Invoker) handleSubtitleSyncStart(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL      string `json:"url"`
		Pages    []int  `json:"pages"`
		CourseID string `json:"courseId"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URL == "" {
		return nil, contract.InputInvalid("所选分集无效")
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	if len([]rune(input.Category)) > 100 {
		return nil, contract.InputInvalid("分类名称无效")
	}
	return inv.runJob("字幕同步知识库", func(ctx context.Context) (any, error) {
		series, err := mediaBilibili.FetchVideoSeries(ctx, nil, input.URL)
		if err != nil {
			return nil, err
		}
		episodes, err := selectedEpisodes(series, input.Pages)
		if err != nil {
			return nil, err
		}
		items := make([]subtitlesync.CreateItem, 0, len(episodes))
		for _, episode := range episodes {
			items = append(items, subtitlesync.CreateItem{Page: episode.Page, Title: episode.Title, URL: episode.URL})
		}
		task, err := subtitleSyncService(state).Create(items, strings.TrimSpace(input.Category), courseName)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return subtitleSyncService(state).Run(ctx, task.ID, inv.subtitleSyncDependencies(store, state, courseName))
	})
}

func (inv *Invoker) handleSubtitleSyncMixed(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Selections []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
			Pages []int  `json:"pages"`
		} `json:"selections"`
		CourseID string `json:"courseId"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(params, &input); err != nil || len(input.Selections) < 1 || len(input.Selections) > 100 {
		return nil, contract.InputInvalid("所选分集无效")
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	return inv.runJob("批量字幕同步知识库", func(ctx context.Context) (any, error) {
		items := []subtitlesync.CreateItem{}
		seen := map[string]bool{}
		for _, selection := range input.Selections {
			series, err := mediaBilibili.FetchVideoSeries(ctx, nil, selection.URL)
			if err != nil {
				return nil, err
			}
			episodes, err := selectedEpisodes(series, selection.Pages)
			if err != nil {
				return nil, err
			}
			prefix := strings.TrimSpace(selection.Title)
			if prefix == "" {
				prefix = series.Title
			}
			for _, episode := range episodes {
				title := []rune(prefix + " · " + episode.Title)
				if len(title) > 200 {
					title = title[:200]
				}
				locator, err := subtitlesync.Locator(episode.URL)
				if err != nil {
					return nil, err
				}
				if seen[locator] {
					return nil, contract.InputInvalid("分集重复或所选总数超过 1000")
				}
				seen[locator] = true
				items = append(items, subtitlesync.CreateItem{
					Page: episode.Page, Title: string(title), URL: episode.URL,
				})
			}
			if len(items) > 1000 {
				return nil, contract.InputInvalid("分集重复或所选总数超过 1000")
			}
		}
		task, err := subtitleSyncService(state).Create(items, strings.TrimSpace(input.Category), courseName)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return subtitleSyncService(state).Run(ctx, task.ID, inv.subtitleSyncDependencies(store, state, courseName))
	})
}

func (inv *Invoker) handleSubtitleSyncResume(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("同步记录无效")
	}
	return inv.runJob("字幕同步知识库", func(ctx context.Context) (any, error) {
		task, err := subtitleSyncService(state).Read(input.ID)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return subtitleSyncService(state).Run(ctx, input.ID, inv.subtitleSyncDependencies(store, state, task.CourseName))
	})
}

func (inv *Invoker) handleSubtitleSyncAttach(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID   string          `json:"id"`
		URL  string          `json:"url"`
		Page int             `json:"page"`
		Raw  json.RawMessage `json:"-"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("同步记录无效")
	}
	var selector any
	var raw struct {
		URL  *string `json:"url"`
		Page *int    `json:"page"`
	}
	_ = json.Unmarshal(params, &raw)
	if raw.URL != nil {
		selector = *raw.URL
	} else if raw.Page != nil {
		selector = *raw.Page
	} else {
		return nil, contract.InputInvalid("请按视频网址明确选择本次同步的分集")
	}
	return inv.runJob("指定同步字幕", func(ctx context.Context) (any, error) {
		task, err := subtitleSyncService(state).Read(input.ID)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", task.CourseName)
		if err != nil {
			return nil, err
		}
		reverse := store.Reverse()
		if reverse == nil {
			return nil, contract.Unavailable("文件选择器不可用")
		}
		raw, err := reverse.Call("shell.pickFile", map[string]any{
			"title": "选择已有字幕", "extensions": []string{"srt"}, "defaultPath": directory,
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
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4000000 {
			return nil, contract.InputInvalid("字幕文件过大")
		}
		body, err := os.ReadFile(*picked.Path)
		if err != nil {
			return nil, contract.InputInvalid("字幕文件不可读")
		}
		path := *picked.Path
		if _, err := library.ManagedFile(state.LibraryRoot, path); err != nil {
			path, err = library.ReserveFile(directory, filepath.Base(path))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, body, 0o644); err != nil {
				_ = os.Remove(path)
				return nil, contract.Unavailable("字幕保存校验失败")
			}
			if check, err := os.ReadFile(path); err != nil || string(check) != string(body) {
				_ = os.Remove(path)
				return nil, contract.Unavailable("字幕保存校验失败")
			}
		}
		if _, err := documents.ParseSrt(string(body)); err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		if _, err := subtitleSyncService(state).Attach(input.ID, selector, path); err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return subtitleSyncService(state).Run(ctx, input.ID, inv.subtitleSyncDependencies(store, state, task.CourseName))
	})
}
