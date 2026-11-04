package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

func (run *correctionRun) cachedBatch(ctx context.Context, batch learning.CorrectionBatch, index int) ([]learning.CorrectedSegment, error) {
	return run.withCachedBatch(ctx, batch, index, run.runBatch)
}

func (run *correctionRun) withCachedBatch(ctx context.Context, batch learning.CorrectionBatch, index int, paid func(context.Context, learning.CorrectionBatch, int) ([]learning.CorrectedSegment, error)) ([]learning.CorrectedSegment, error) {
	if run.requestLedger == nil {
		return paid(ctx, batch, index)
	}
	key := fmt.Sprintf("correction:%d:%d", batch.Start, len(batch.Rows))
	input, images := correctionEffectiveRequest(run, batch, index)
	wireDigest, err := run.client.JSONRequestDigest(ctx, run.prompt, input, ai.JSONOptions{Images: images})
	if err != nil {
		return nil, err
	}
	version := 1
	if run.compact {
		version = 2
		if run.compactVersion >= compactProtocolOneBased {
			version = 3
		}
	}
	identityBody, _ := json.Marshal(struct {
		RecipeVersion int
		Fingerprint   string
		Batch         learning.CorrectionBatch
		WireDigest    string
		RepairPolicy  any
	}{version, run.fingerprint, batch, wireDigest, ai.JSONRecipePolicy()})
	identityHash := sha256.Sum256(identityBody)
	identity := hex.EncodeToString(identityHash[:])
	body, found, err := run.requestLedger.AdoptSuccess(ctx, run.requestFence, key, identity)
	if err != nil {
		return nil, err
	}
	if found {
		rows, err := learning.ParseCorrectedSegments(string(body))
		if err != nil || len(rows) != len(batch.Rows) {
			return nil, fmt.Errorf("校订成功缓存结构无效")
		}
		for index, row := range rows {
			source := batch.Rows[index]
			if row.Start != source.Start || row.End != source.End || row.OriginalText != source.Text {
				return nil, fmt.Errorf("校订成功缓存与原文不一致")
			}
		}
		guarded, guardErr := learning.GuardCorrectedSegments(rows, batch.Rows)
		if guardErr != nil {
			return nil, guardErr
		}
		for index := range guarded {
			if reason := learning.CorrectionFidelityReason(
				batch.Rows[index].Text, guarded[index].Text, run.sourceSegments,
			); reason != "" {
				guarded[index].Text = batch.Rows[index].Text
				guarded[index].Uncertain = true
				guarded[index].Reason = reason
			}
		}
		if run.compact && run.compactVersion == compactProtocolHistorical {
			guarded = learning.QuarantineCompactChanges(guarded)
		}
		return guarded, nil
	}
	rows, err := paid(ctx, batch, index)
	if err != nil {
		return nil, err
	}
	body, err = json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("校订成功结果编码失败")
	}
	// Preserve an already validated paid success even if a sibling failed and
	// cancelled the execution context. The generation/epoch fence still applies.
	cacheContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := run.requestLedger.SaveSuccess(cacheContext, run.requestFence, key, identity, body); err != nil {
		return nil, err
	}
	return rows, nil
}
