package subtitlederived

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type ModeRecipe struct {
	Version            int                           `json:"version"`
	Mode               string                        `json:"mode"`
	SystemPromptSHA256 string                        `json:"systemPromptSHA256"`
	ChunkPolicy        string                        `json:"chunkPolicy"`
	ChunkLimit         int                           `json:"chunkLimit"`
	MaxPoints          int                           `json:"maxPoints"`
	JSONPolicy         contract.JSONGenerationPolicy `json:"jsonPolicy"`
}
type TaskRecipe struct {
	Version       int                                `json:"version"`
	Origin        string                             `json:"origin"`
	Configuration contract.RequestedGenerationConfig `json:"configuration"`
	Hash          string                             `json:"hash"`
}
type ItemRecipe struct {
	Version        int        `json:"version"`
	Origin         string     `json:"origin"`
	TaskRecipeHash string     `json:"taskRecipeHash"`
	SourceSHA256   string     `json:"sourceSHA256"`
	ModeRecipe     ModeRecipe `json:"modeRecipe"`
	Hash           string     `json:"hash"`
}

var recipeHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func recipeHash(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return digest(string(body))
}
func taskRecipeHash(config contract.RequestedGenerationConfig, course string) string {
	return recipeHash(struct {
		Configuration contract.RequestedGenerationConfig
		Course        string
	}{config, course})
}
func itemRecipeHash(value ItemRecipe) string { value.Hash = ""; return recipeHash(value) }
func canBindRecipe(item Item) bool {
	return item.Prepared == nil && len(item.Outputs) == 0 && item.GenerationState == ""
}

func validRequestedConfig(value contract.RequestedGenerationConfig) bool {
	if value.Version != 1 || (value.APIFormat != "chat" && value.APIFormat != "responses") || !recipeHashPattern.MatchString(value.EndpointSHA256) || strings.TrimSpace(value.RequestedModel) == "" || len(value.RequestedModel) > 1024 || value.ServerModelState != "unknown" || value.AccountScopeState != "unknown" {
		return false
	}
	for key, parameter := range value.EffectiveParameters {
		switch key {
		case "temperature", "top_p", "max_tokens", "max_output_tokens":
			encoded, err := json.Marshal(parameter)
			if err != nil || len(encoded) == 0 || string(encoded) == "null" {
				return false
			}
			var number float64
			if json.Unmarshal(encoded, &number) != nil {
				return false
			}
		case "reasoning_effort":
			if _, ok := parameter.(string); !ok {
				return false
			}
		case "reasoning":
			body, err := json.Marshal(parameter)
			if err != nil {
				return false
			}
			var fields map[string]string
			if json.Unmarshal(body, &fields) != nil || len(fields) != 1 || fields["effort"] == "" {
				return false
			}
		default:
			return false
		}
	}
	return recipeHash(value) != ""
}
func validModeRecipe(value ModeRecipe, mode string) bool {
	policy := (value.Version == 1 && value.ChunkPolicy == "subtitle-text-runes") || (value.Version == 2 && value.ChunkPolicy == "subtitle-core-codepoints-v2")
	return policy && value.Mode == mode && validMode(mode) && recipeHashPattern.MatchString(value.SystemPromptSHA256) && value.ChunkLimit > 0 && value.MaxPoints > 0 && value.JSONPolicy.Version == 1 && value.JSONPolicy.Encoding == "json-no-html-escape" && value.JSONPolicy.MaxAttempts > 0 && recipeHashPattern.MatchString(value.JSONPolicy.RepairFeedbackSHA256)
}
func (service *Service) bindRecipe(ctx context.Context, task *Task, dependencies Dependencies) error {
	if dependencies.ResolveRecipes == nil {
		return fmt.Errorf("摘要生成配方不可用，未调用模型")
	}
	modes := []string{}
	seen := map[string]bool{}
	for _, item := range task.Items {
		if canBindRecipe(item) && !seen[item.Mode] {
			modes = append(modes, item.Mode)
			seen[item.Mode] = true
		}
	}
	versions := map[string]int{}
	for _, item := range task.Items {
		if item.Recipe != nil {
			versions[item.Mode] = item.Recipe.ModeRecipe.Version
		}
	}
	configuration, recipes, err := dependencies.ResolveRecipes(contract.WithSummaryRecipeVersions(ctx, versions), modes)
	if err != nil {
		return err
	}
	if !validRequestedConfig(configuration) {
		return fmt.Errorf("摘要生成配置快照无效，未调用模型")
	}
	hash := taskRecipeHash(configuration, task.CourseName)
	if task.EvidenceVersion != 0 && task.EvidenceVersion != 1 {
		return fmt.Errorf("摘要证据版本不受支持，原任务保留")
	}
	firstBinding := task.Recipe == nil
	candidate := *task
	candidate.Items = append([]Item(nil), task.Items...)
	if !firstBinding {
		if task.EvidenceVersion != 1 || task.Recipe.Version != 1 || task.Recipe.Origin != dispatchFirstNew || !validRequestedConfig(task.Recipe.Configuration) || task.Recipe.Hash != taskRecipeHash(task.Recipe.Configuration, task.CourseName) || task.Recipe.Hash != hash {
			return fmt.Errorf("摘要生成配置已改变，请新建任务；原成果保留")
		}
	} else {
		if task.EvidenceVersion != 0 {
			return fmt.Errorf("摘要生成配方记录缺失，原任务保留")
		}
		candidate.EvidenceVersion = 1
		candidate.Recipe = &TaskRecipe{Version: 1, Origin: dispatchFirstNew, Configuration: configuration, Hash: hash}
	}
	for index := range candidate.Items {
		item := &candidate.Items[index]
		if !canBindRecipe(*item) {
			continue
		}
		modeRecipe, ok := recipes[item.Mode]
		if !ok || !validModeRecipe(modeRecipe, item.Mode) {
			return fmt.Errorf("摘要提示词或分块配方无效，未调用模型")
		}
		wanted := ItemRecipe{Version: 1, Origin: dispatchFirstNew, TaskRecipeHash: hash, SourceSHA256: item.Source.Hash, ModeRecipe: modeRecipe}
		wanted.Hash = itemRecipeHash(wanted)
		if item.Recipe != nil {
			if item.Recipe.Hash != itemRecipeHash(*item.Recipe) || item.Recipe.Hash != wanted.Hash {
				return fmt.Errorf("摘要提示词或分块配置已改变，请新建任务；原成果保留")
			}
		} else if !firstBinding {
			return fmt.Errorf("摘要分集配方记录缺失，原任务保留")
		} else {
			item.Recipe = &wanted
		}
	}
	if err := service.save(candidate); err != nil {
		return err
	}
	task.EvidenceVersion, task.Recipe = candidate.EvidenceVersion, candidate.Recipe
	for index := range task.Items {
		task.Items[index].Recipe = candidate.Items[index].Recipe
	}
	return nil
}

const dispatchFirstNew = "first-new-dispatch"
