package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/EveSunMaple/Zynote/backend/internal/asr"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"testing"
	"time"
)

func TestVideoPptErrorPreservesDirectWireSafeClassification(t *testing.T) {
	for _, code := range []string{contract.CodeAuthExpired, contract.CodeRateLimited, contract.CodeCancelled, contract.CodeTimeout, contract.CodeNoSubtitle} {
		original := contract.NewError(code, "controlled failure")
		actual := videoPptError(fmt.Errorf("controlled wrapper: %w", original))
		if actual != original {
			t.Fatal("classification flattened", actual)
		}
	}
	for _, test := range []struct {
		err  error
		code string
	}{{context.Canceled, contract.CodeCancelled}, {context.DeadlineExceeded, contract.CodeTimeout}} {
		actual, ok := videoPptError(fmt.Errorf("scan: %w", test.err)).(*contract.AppError)
		if !ok || actual.Code != test.code {
			t.Fatalf("context flattened %+v", actual)
		}
	}
}

func TestASRPipelineContextReachesVideoAPICode(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		var ctx context.Context
		var cancel context.CancelFunc
		expected := contract.CodeCancelled
		if timeout {
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			expected = contract.CodeTimeout
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		}
		pipeline := asr.Pipeline{Call: func(context.Context, string, any, int) (json.RawMessage, error) {
			t.Fatal("cancelled request dispatched native")
			return nil, nil
		}}
		_, err := pipeline.Transcribe(ctx, "unused", "en")
		cancel()
		mapped, ok := videoPptError(err).(*contract.AppError)
		if !ok || mapped.Code != expected {
			t.Fatalf("pipeline/API context lost %+v", mapped)
		}
	}
}
