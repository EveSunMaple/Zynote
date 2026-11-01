package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/visual"
)

func TestVisualErrorPreservesContextAndTypedOutcomes(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{fmt.Errorf("scan: %w", context.Canceled), contract.CodeCancelled},
		{fmt.Errorf("probe: %w", context.DeadlineExceeded), contract.CodeTimeout},
		{fmt.Errorf("capture: %w", contract.NewError(contract.CodeCancelled, "cancelled")), contract.CodeCancelled},
		{&visual.RequestError{Message: "invalid source"}, contract.CodeInputInvalid},
		{fmt.Errorf("scan failed"), contract.CodeUnavailable},
	} {
		mapped, ok := visualError(test.err).(*contract.AppError)
		if !ok || mapped.Code != test.code {
			t.Fatalf("wrong mapping: %+v", mapped)
		}
	}
}
