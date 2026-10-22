package api

import (
	"encoding/json"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

func (inv *Invoker) handleDocumentParse(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Path == "" {
		return nil, contract.InputInvalid("文档路径无效")
	}
	parsed, err := documents.ParseDocument(inv.native, input.Path)
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

func (inv *Invoker) handleTranscriptRead(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Path == "" {
		return nil, contract.InputInvalid("原文路径无效")
	}
	result, err := documents.ReadTranscript(inv.native, input.Path)
	if err != nil {
		return nil, err
	}
	return result, nil
}
