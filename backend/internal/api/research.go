package api

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/research"
)

func (inv *Invoker) researchStore(state *contract.State) (*research.Store, error) {
	store, err := research.Open(filepath.Join(state.LibraryRoot, "知识库数据"))
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	return store, nil
}

func (inv *Invoker) researchEngine(store contract.StateStore, state *contract.State, data *research.Store) (*research.Engine, error) {
	knowledgeStore, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	connection, err := config.CurrentConnectionFingerprint(state.DataRoot)
	if err != nil {
		return nil, err
	}
	return &research.Engine{
		Store: data, Sources: research.NewSources(knowledgeStore, data.RootID), Connection: connection,
		Model: func(ctx context.Context, system, user string) (string, error) {
			profile, err := config.ActiveProfile(state.DataRoot, store.Reverse())
			if err != nil {
				return "", err
			}
			client := ai.New(ai.Profile{
				BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
				APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
			})
			return client.Chat(ctx, []ai.Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user},
			}, nil)
		},
	}, nil
}

func (inv *Invoker) handleToolsRequest(storeState contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Action string         `json:"action"`
		Args   map[string]any `json:"args"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Action == "" {
		return nil, contract.InputInvalid("研究操作无效")
	}
	if input.Args == nil {
		input.Args = map[string]any{}
	}
	store, err := inv.researchStore(state)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	switch input.Action {
	case "materials":
		knowledgeStore, err := inv.knowledgeStoreFor(state)
		if err != nil {
			return nil, err
		}
		sources := research.NewSources(knowledgeStore, store.RootID)
		return sources.Catalog(textValue(input.Args["query"]), int(numberValue(input.Args["offset"])))
	case "conversations":
		return store.Conversations(int(numberValue(input.Args["offset"])))
	case "conversation-create":
		return store.CreateConversation(textValue(input.Args["title"]))
	case "conversation-read":
		return store.Conversation(textValue(input.Args["id"]))
	case "conversation-organization":
		value, err := store.Conversation(textValue(input.Args["id"]))
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return map[string]any{
			"revision": value["revision"], "pinned": value["pinned"], "title": value["title"],
		}, nil
	case "conversation-organize":
		result, err := store.OrganizeConversation(input.Args)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "memories":
		return store.Memories()
	case "memory-change":
		result, err := store.ChangeMemory(input.Args["value"], input.Args["deleting"] == true)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "extension-change":
		result, err := store.ChangeExtensions(input.Args)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "skill-markdown":
		result, err := research.ParseSkillMarkdown(textValue(input.Args["text"]))
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "notes":
		result, err := store.Notes(textValue(input.Args["query"]), textValue(input.Args["state"]), int(numberValue(input.Args["offset"])))
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "note-read":
		result, err := store.NoteRead(textValue(input.Args["id"]))
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "note-revise":
		value, ok := input.Args["value"].(map[string]any)
		if !ok {
			return nil, contract.InputInvalid("笔记修订参数无效")
		}
		result, err := store.ReviseNote(value)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "note-lifecycle":
		value, ok := input.Args["value"].(map[string]any)
		if !ok {
			return nil, contract.InputInvalid("笔记状态参数无效")
		}
		result, err := store.LifecycleNote(value)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "note-source":
		result, err := store.NoteSource(textValue(input.Args["id"]))
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "note-export":
		result, err := store.ExportNote(textValue(input.Args["id"]))
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return result, nil
	case "agent-create", "agent-read", "agent-stop", "agent-export", "memory-accept", "note-preview", "note-save",
		"workflow-create", "workflow-action", "workflow-export":
		engine, err := inv.researchEngine(storeState, state, store)
		if err != nil {
			return nil, err
		}
		switch input.Action {
		case "agent-create":
			return engine.Create(context.Background(), input.Args)
		case "agent-read":
			return engine.Read(textValue(input.Args["id"]))
		case "agent-stop":
			return engine.Stop(textValue(input.Args["id"]))
		case "agent-export":
			return engine.Export(textValue(input.Args["id"]))
		case "memory-accept":
			value, _ := input.Args["value"].(map[string]any)
			return engine.AcceptMemory(textValue(input.Args["id"]), value)
		case "note-preview":
			return engine.PreviewNote(textValue(input.Args["id"]))
		case "note-save":
			value, _ := input.Args["expected"].(map[string]any)
			return engine.SaveNote(textValue(input.Args["id"]), value)
		case "workflow-create":
			return engine.CreateWorkflow(context.Background(), input.Args)
		case "workflow-action":
			return engine.ActionWorkflow(context.Background(), input.Args)
		default:
			return engine.ExportWorkflow(textValue(input.Args["id"]))
		}
	case "overview":
		extensions, _ := store.Extensions()
		engine, _ := inv.researchEngine(storeState, state, store)
		tasks := []any{}
		workflows := []any{}
		if engine != nil {
			tasks = engine.List()
			workflows = engine.ListWorkflows()
		}
		return map[string]any{
			"root": store.RootID, "extensions": extensions, "mcp": map[string]any{"revision": 0, "servers": []any{}},
			"tasks": tasks, "workflows": workflows,
			"migration": map[string]any{
				"runtimeLanguage": "go", "legacyRuntimeRequired": false,
				"externalAcceptance": "研究引擎/MCP/IM 尚未迁移，当前仅提供存储与资料目录",
			},
			"runtime": map[string]any{"dependencies": []any{}},
		}, nil
	default:
		return nil, contract.Unavailable("此研究工具尚未完成原生适配：" + input.Action)
	}
}
