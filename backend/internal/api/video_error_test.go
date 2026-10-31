package api

import (
	"context"
	"fmt"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"testing"
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
