package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
	"github.com/EveSunMaple/Zynote/backend/internal/summarytree"
)

type stagedSummaryJSON struct {
	Version    int
	Body       json.RawMessage
	DecodedRef contract.GenerationEventRef
}

func runSummaryStage(ctx context.Context, client *ai.Client, purpose string, index int, prompt string, input any, validate func([]byte) error) (json.RawMessage, summaryglobal.StageRef, error) {
	factory := contract.SummaryStagesFrom(ctx)
	if factory == nil {
		return nil, summaryglobal.StageRef{}, fmt.Errorf("独立摘要阶段不可用")
	}
	body, err := summaryglobal.Encode(input)
	if err != nil || len(body) > summarytree.MaxNodeBytes {
		return nil, summaryglobal.StageRef{}, fmt.Errorf("独立摘要阶段输入超过页面预算")
	}
	inputHash, promptHash := summarytree.Digest(body), summarytree.Digest([]byte(prompt))
	stageContext, err := factory.Open(ctx, purpose, index, inputHash, promptHash)
	if err != nil {
		return nil, summaryglobal.StageRef{}, err
	}
	metadata, observer := contract.GenerationObservationFrom(stageContext)
	digestMetadata := metadata
	digestMetadata.AttemptIndex = 0
	empty := sha256.Sum256(nil)
	digestMetadata.FeedbackSHA256 = hex.EncodeToString(empty[:])
	wire, err := client.JSONRequestDigest(contract.WithGenerationObservation(stageContext, digestMetadata, observer), prompt, input, ai.JSONOptions{})
	if err != nil {
		return nil, summaryglobal.StageRef{}, err
	}
	encoded, _ := summaryglobal.Encode(struct {
		Scope  contract.GenerationObservation
		Wire   string
		Policy any
	}{metadata, wire, ai.JSONRecipePolicy()})
	identity := summarytree.Digest(encoded)
	cache := contract.SuccessCacheFrom(ctx)
	if cache == nil {
		return nil, summaryglobal.StageRef{}, fmt.Errorf("独立摘要阶段缺少持久缓存")
	}
	key := "stage:" + metadata.StageID
	complete := func(output []byte, receipt contract.GenerationReceipt) (json.RawMessage, summaryglobal.StageRef, error) {
		if receipt.Event.State != "format-result" || receipt.Event.FormatValid == nil || !*receipt.Event.FormatValid || receipt.Ref.Version != 4 || receipt.Event.JSONBodySHA256 != summarytree.Digest(output) {
			return nil, summaryglobal.StageRef{}, contract.ObservationFailure(fmt.Errorf("独立阶段响应与格式回执不匹配"))
		}
		return append(json.RawMessage(nil), output...), summaryglobal.StageRef{ID: metadata.StageID, Index: index, Purpose: purpose, InputSHA256: inputHash, JSONSHA256: summarytree.Digest(output), Format: receipt.Ref}, nil
	}
	raw, found, err := cache.Adopt(stageContext, key, identity)
	if err != nil {
		return nil, summaryglobal.StageRef{}, err
	}
	if found {
		var saved stagedSummaryJSON
		if summaryglobal.Decode(raw, &saved) != nil || saved.Version != 1 || validate(saved.Body) != nil {
			return nil, summaryglobal.StageRef{}, contract.ObservationFailure(fmt.Errorf("独立摘要阶段缓存无效"))
		}
		receipt, err := contract.ReadGenerationReceipt(stageContext)
		if err != nil {
			return nil, summaryglobal.StageRef{}, err
		}
		if receipt.Event.State == "response-decoded" && receipt.Ref == saved.DecodedRef {
			event := receipt.Event
			event.State = "format-result"
			valid := true
			event.FormatValid = &valid
			event.JSONBodySHA256 = summarytree.Digest(saved.Body)
			if err := observer(stageContext, event); err != nil {
				return nil, summaryglobal.StageRef{}, contract.ObservationFailure(err)
			}
			receipt, err = contract.ReadGenerationReceipt(stageContext)
			if err != nil {
				return nil, summaryglobal.StageRef{}, err
			}
		}
		if receipt.PreviousRef != saved.DecodedRef {
			return nil, summaryglobal.StageRef{}, contract.ObservationFailure(fmt.Errorf("独立阶段缓存原响应引用不匹配"))
		}
		return complete(saved.Body, receipt)
	}
	output, err := client.JSON(stageContext, prompt, input, func(output []byte) error {
		if len(output) > summarytree.MaxNodeBytes {
			return fmt.Errorf("独立摘要阶段响应超过页面预算")
		}
		if err := validate(output); err != nil {
			return err
		}
		local, cancel := context.WithTimeout(context.WithoutCancel(stageContext), 5*time.Second)
		defer cancel()
		receipt, err := contract.ReadGenerationReceipt(local)
		if err != nil {
			return contract.ObservationFailure(err)
		}
		if receipt.Event.State != "response-decoded" || receipt.Ref.Version != 4 {
			return contract.ObservationFailure(fmt.Errorf("独立阶段缺少原响应记录"))
		}
		body, err := summaryglobal.Encode(stagedSummaryJSON{1, append(json.RawMessage(nil), output...), receipt.Ref})
		if err != nil {
			return contract.ObservationFailure(err)
		}
		return contract.ObservationFailure(cache.Save(local, key, identity, body))
	})
	if err != nil {
		return nil, summaryglobal.StageRef{}, err
	}
	receipt, err := contract.ReadGenerationReceipt(stageContext)
	if err != nil {
		return nil, summaryglobal.StageRef{}, err
	}
	return complete(output, receipt)
}
