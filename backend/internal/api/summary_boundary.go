package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func proposeSummaryBoundary(ctx context.Context, client *ai.Client, index int, input any) ([]byte, summaryglobal.StageRef, error) {
	return runSummaryStage(ctx, client, "summary-boundary", index, summaryinput.BoundaryPrompt, input, func(body []byte) error {
		if len(body) > 16<<10 || !json.Valid(body) {
			return fmt.Errorf("边界提议JSON超限或无效")
		}
		return nil
	})
}
