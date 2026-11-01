package subtitlederived

import (
	"context"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func TestV2ReceiptReaderReturnsOnlyIndependentDurableSnapshots(t *testing.T) {
	service, task := recoveryTask(t)
	if err := service.bindRecipe(context.Background(), &task, Dependencies{ResolveRecipes: fixtureRecipes}); err != nil {
		t.Fatal(err)
	}
	ctx, err := service.observationContext(context.Background(), &task, &task.Items[0], "1\n00:00:01,000 --> 00:00:02,000\n第一句\n")
	if err != nil {
		t.Fatal(err)
	}
	metadata, observer := contract.GenerationObservationFrom(ctx)
	metadata.ChunkIndex = 0
	metadata.ChunkCount = 1
	metadata.AttemptIndex = 0
	metadata.CueIDs = []int{0}
	metadata.InputSHA256 = digest("input")
	metadata.FeedbackSHA256 = digest("")
	event := contract.GenerationAttemptEvidence{Version: 2, Observation: metadata, State: "dispatch-intent", ConfigurationSHA256: recipeHash(task.Recipe.Configuration), EndpointSHA256: task.Recipe.Configuration.EndpointSHA256, SystemPromptSHA256: task.Items[0].Recipe.ModeRecipe.SystemPromptSHA256, RequestBodySHA256: digest("body")}
	if err := observer(ctx, event); err != nil {
		t.Fatal(err)
	}
	first, err := contract.ReadGenerationReceipt(ctx)
	if err != nil || first.Ref.Sequence != 1 || first.Ref.SHA256 != task.Items[0].AttemptEvidence.HeadSHA256 {
		t.Fatal(first, err)
	}
	first.Event.Observation.CueIDs[0] = 999
	second, err := contract.ReadGenerationReceipt(ctx)
	if err != nil || second.Event.Observation.CueIDs[0] != 0 {
		t.Fatal("receipt caller mutated durable observer snapshot", second, err)
	}
}
func TestV2AuditRejectsProtocolMixAndWrongAcceptedRef(t *testing.T) {
	for _, change := range []string{"mixed-event", "mixed-record", "wrong-ref"} {
		t.Run(change, func(t *testing.T) {
			service, task := completeAuditFixture(t)
			rewriteAuditChain(t, service, task, func(records []attemptRecord) {
				if change == "mixed-event" {
					records[1].Event.Version = 2
					records[1].Event.Observation.ProtocolVersion = 2
				} else if change == "mixed-record" {
					records[1].Version = 2
				} else {
					records[3].Event.State = "validated-points"
					records[3].Event.Version = 2
					records[3].Event.Observation.ProtocolVersion = 2
					records[3].Event.ValidatedPoints = &contract.ValidatedPointEvidence{AcceptedFormatRef: contract.GenerationEventRef{Version: 2, Sequence: 2, SHA256: digest("other")}, PointsSHA256: digest("[]")}
				}
			})
			if audit := service.AuditAttemptEvidence(context.Background(), *task, 0); audit.State != "invalid" {
				t.Fatal("mixed/forged lineage accepted", audit)
			}
		})
	}
}
