package api

import (
	"encoding/json"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
)

func (inv *Invoker) knowledgeStoreFor(state *contract.State) (*knowledge.Store, error) {
	root := filepath.Join(state.LibraryRoot, "知识库数据")
	inv.mediaMu.Lock()
	defer inv.mediaMu.Unlock()
	if inv.knowledge != nil && inv.knowledgeRoot == root {
		return inv.knowledge, nil
	}
	store, err := knowledge.Open(root)
	if err != nil {
		return nil, contract.Unavailable("知识库不可用")
	}
	inv.knowledge = store
	inv.knowledgeRoot = root
	return store, nil
}

func (inv *Invoker) handleKnowledgeList(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	return service.List()
}

func (inv *Invoker) handleKnowledgeGeneration(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Locator string `json:"locator"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("资料来源地址无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	return service.Generation(input.Locator)
}

func (inv *Invoker) handleKnowledgeRead(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.ID == "" {
		return nil, contract.InputInvalid("资料标识无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.Read(input.ID)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleKnowledgeImport(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input knowledge.ImportInput
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("资料导入参数无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.Import(input)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleKnowledgeCitation(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		EvidenceID string `json:"evidenceId"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("引用编号无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.Citation(input.EvidenceID)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleKnowledgeDelete(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID         string `json:"id"`
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("资料标识无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.Delete(input.ID, input.Generation)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleKnowledgeOrganize(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Items  []knowledge.OrganizeItem `json:"items"`
		Target string                   `json:"target"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("整理参数无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.Organize(input.Items, input.Target)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleKnowledgeRebuild(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	return service.Rebuild()
}

func (inv *Invoker) handleKnowledgeSearch(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Query string   `json:"query"`
		IDs   []string `json:"ids"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("搜索内容无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.Search(input.Query, input.IDs)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleKnowledgeClassificationOptions(_ contract.StateStore, _ *contract.State, _ json.RawMessage) (any, error) {
	return knowledge.ClassificationOptions, nil
}

func (inv *Invoker) handleKnowledgeClassificationRead(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.ID == "" {
		return nil, contract.InputInvalid("资料标识无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.ClassificationRead(input.ID)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func (inv *Invoker) handleKnowledgeClassificationSave(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Token     string          `json:"token"`
		Selection json.RawMessage `json:"selection"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Token == "" || len(input.Selection) == 0 {
		return nil, contract.InputInvalid("分类草稿已失效，请重新读取资料")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.ClassificationSave(input.Token, input.Selection)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}
