package transport

import (
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"testing"
)

func TestNoSubtitleOutcomeKeepsWireClassification(t *testing.T) {
	wire := toWireError(contract.NewError(contract.CodeNoSubtitle, "此分集没有站内字幕"))
	if wire.Code != contract.CodeNoSubtitle || wire.Message != "此分集没有站内字幕" {
		t.Fatal(wire)
	}
}
