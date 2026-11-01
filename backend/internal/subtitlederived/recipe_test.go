package subtitlederived

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func fixtureRecipes(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]ModeRecipe, error) {
	configuration := contract.RequestedGenerationConfig{Version: 1, APIFormat: "chat", EndpointSHA256: strings.Repeat("a", 64), RequestedModel: "fixture-model", EffectiveParameters: map[string]any{}, ServerModelState: "unknown", AccountScopeState: "unknown"}
	recipes := map[string]ModeRecipe{}
	for _, mode := range modes {
		recipes[mode] = ModeRecipe{Version: 1, Mode: mode, SystemPromptSHA256: digest("fixture prompt:" + mode), ChunkPolicy: "subtitle-text-runes", ChunkLimit: 15000, MaxPoints: 14, JSONPolicy: contract.JSONGenerationPolicy{Version: 1, Encoding: "json-no-html-escape", MaxAttempts: 2, RepairFeedbackSHA256: digest("fixture repair")}}
	}
	return configuration, recipes, nil
}

func TestRecipeFreezesEveryPendingModeBeforeFirstDispatch(t *testing.T) {
	for _, change := range []string{"model", "endpoint", "parameters", "prompt", "chunk", "missing-item"} {
		t.Run(change, func(t *testing.T) {
			service, initial := recoveryTask(t)
			task, err := service.Create([]Source{initial.Items[0].Source}, []string{"brief", "detailed"}, initial.CourseName)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			dependencies := Dependencies{ResolveRecipes: fixtureRecipes, Summarize: func(context.Context, string, string) (string, []Point, error) {
				calls++
				disk, err := service.Read(task.ID)
				if err != nil || disk.EvidenceVersion != 1 || disk.Recipe == nil || disk.Items[0].Recipe == nil || disk.Items[1].Recipe == nil || disk.Items[0].GenerationState != "dispatched" {
					t.Fatal("recipes not fully durable before dispatch", disk, err)
				}
				cancel()
				return "# First mode received", nil, nil
			}}
			_, _ = service.Run(ctx, task.ID, dependencies)
			cancel()
			if calls != 1 {
				t.Fatal("unexpected dispatch count", calls)
			}
			if change == "missing-item" {
				disk, _ := service.Read(task.ID)
				disk.Items[1].Recipe = nil
				if err := service.save(disk); err != nil {
					t.Fatal(err)
				}
			}
			dependencies.ResolveRecipes = func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]ModeRecipe, error) {
				configuration, recipes, err := fixtureRecipes(ctx, modes)
				switch change {
				case "model":
					configuration.RequestedModel = "different-model"
				case "endpoint":
					configuration.EndpointSHA256 = strings.Repeat("b", 64)
				case "parameters":
					configuration.EffectiveParameters["temperature"] = 0.5
				case "prompt":
					value := recipes["detailed"]
					value.SystemPromptSHA256 = digest("new prompt")
					recipes["detailed"] = value
				case "chunk":
					value := recipes["detailed"]
					value.ChunkLimit = 14000
					recipes["detailed"] = value
				}
				return configuration, recipes, err
			}
			result, err := service.Run(context.Background(), task.ID, dependencies)
			if err != nil || calls != 1 || result.Items[1].Status != "失败" || result.Items[1].GenerationState != "" {
				t.Fatal("configuration drift dispatched", result, calls, err)
			}
		})
	}
}

func TestRecipeSaveFailureStopsProviderAndRemainsExplicitlyRecoverable(t *testing.T) {
	service, task := recoveryTask(t)
	calls := 0
	blocker := filepath.Join(service.Root, task.ID+".json.tmp")
	dependencies := Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]ModeRecipe, error) {
		if err := os.Mkdir(blocker, 0700); err != nil {
			t.Fatal(err)
		}
		return fixtureRecipes(ctx, modes)
	}, Summarize: func(context.Context, string, string) (string, []Point, error) { calls++; return "# Ready", nil, nil }}
	if _, err := service.Run(context.Background(), task.ID, dependencies); err == nil {
		t.Fatal("recipe save failure hidden")
	}
	disk, err := service.Read(task.ID)
	if err != nil || calls != 0 || disk.Recipe != nil || disk.Items[0].GenerationState != "" {
		t.Fatal("failed recipe write dispatched or bound memory", disk, calls, err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	dependencies.ResolveRecipes = fixtureRecipes
	result, err := service.Run(context.Background(), task.ID, dependencies)
	if err != nil || calls != 1 || result.Items[0].Status != "已完成" {
		t.Fatal(result, calls, err)
	}
}

func TestLegacyPreparedAndUnknownDoNotResolveOrInventRecipes(t *testing.T) {
	for _, state := range []string{"prepared", "dispatched"} {
		t.Run(state, func(t *testing.T) {
			service, task := recoveryTask(t)
			task.Items[0].GenerationState = state
			if state == "prepared" {
				task.Items[0].Prepared = &Prepared{Markdown: "# Saved", Points: []Point{{Cue: 0, Quote: "第一句", Point: "结论"}}}
			}
			if err := service.save(task); err != nil {
				t.Fatal(err)
			}
			dependencies := Dependencies{ResolveRecipes: func(context.Context, []string) (contract.RequestedGenerationConfig, map[string]ModeRecipe, error) {
				t.Fatal("legacy history requested current configuration")
				return contract.RequestedGenerationConfig{}, nil, nil
			}, Summarize: func(context.Context, string, string) (string, []Point, error) {
				t.Fatal("legacy history replayed")
				return "", nil, nil
			}}
			result, err := service.Run(context.Background(), task.ID, dependencies)
			if err != nil || result.Recipe != nil || result.EvidenceVersion != 0 || result.Items[0].Recipe != nil {
				t.Fatal("legacy history retrofitted", result, err)
			}
		})
	}
}

func TestRecipeBindingFailureDoesNotMutateInMemoryTask(t *testing.T) {
	service, task := recoveryTask(t)
	blocker := filepath.Join(service.Root, task.ID+".json.tmp")
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	if err := service.bindRecipe(context.Background(), &task, Dependencies{ResolveRecipes: fixtureRecipes}); err == nil {
		t.Fatal("expected save error")
	}
	if task.EvidenceVersion != 0 || task.Recipe != nil || task.Items[0].Recipe != nil {
		t.Fatal("failed save mutated task", task)
	}
}

func TestRecipeNilResolverAndCorruptEvidenceNeverDispatch(t *testing.T) {
	for _, invalid := range []string{"nil", "version", "task-hash", "item-hash"} {
		t.Run(invalid, func(t *testing.T) {
			service, task := recoveryTask(t)
			deps := Dependencies{ResolveRecipes: fixtureRecipes}
			if invalid != "nil" {
				if err := service.bindRecipe(context.Background(), &task, deps); err != nil {
					t.Fatal(err)
				}
				switch invalid {
				case "version":
					task.EvidenceVersion = 99
				case "task-hash":
					task.Recipe.Hash = strings.Repeat("f", 64)
				case "item-hash":
					task.Items[0].Recipe.Hash = strings.Repeat("f", 64)
				}
				if err := service.save(task); err != nil {
					t.Fatal(err)
				}
			} else {
				deps.ResolveRecipes = nil
			}
			calls := 0
			deps.Summarize = func(context.Context, string, string) (string, []Point, error) {
				calls++
				return "# Unexpected", nil, nil
			}
			result, err := service.Run(context.Background(), task.ID, deps)
			if err != nil || calls != 0 || result.Items[0].Status != "失败" || result.Items[0].GenerationState != "" || result.Items[0].Error == "" {
				t.Fatal("invalid evidence dispatched", result, calls, err)
			}
		})
	}
}

func TestSameBoundRecipeCanResumeUncalledItem(t *testing.T) {
	service, initial := recoveryTask(t)
	task, err := service.Create([]Source{initial.Items[0].Source}, []string{"brief", "detailed"}, initial.CourseName)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.bindRecipe(context.Background(), &task, Dependencies{ResolveRecipes: fixtureRecipes}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := service.Run(context.Background(), task.ID, Dependencies{ResolveRecipes: fixtureRecipes, Summarize: func(context.Context, string, string) (string, []Point, error) { calls++; return "# Ready", nil, nil }})
	if err != nil || calls != 2 || result.Items[0].Status != "已完成" || result.Items[1].Status != "已完成" {
		t.Fatal("same recipe refused", result, calls, err)
	}
}
