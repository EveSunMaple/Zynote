package api

import (
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
}
