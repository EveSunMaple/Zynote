package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

func TestPublicationAuditActualAPIIsReadonlyAndFencesExpectedRefs(t *testing.T) {
	service, task, client, calls, server := pointEvidenceFixture(t)
	defer server.Close()
	completed, err := service.Run(context.Background(), task.ID, pointEvidenceDependencies(client))
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: service.Library}
	inv := NewInvoker()
	record := subtitlederived.RecordedPublication(completed.Items[0], completed.OutputVersion)
	input := publicationAuditInput{ID: task.ID, ItemIndex: 0, ExpectedPlanSHA256: record.PlanSHA256, ExpectedReceiptSHA256: record.ReceiptSHA256}
	before, _ := os.ReadFile(filepath.Join(service.Root, task.ID+".json"))
	count := calls.Load()
	raw, _ := json.Marshal(input)
	value, err := inv.handlePublicationAudit(nil, state, raw)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(subtitlederived.PublicationAudit)
	if result.State != "complete" || result.CheckedAt == "" || result.VerifiedPoints != 2 || calls.Load() != count {
		t.Fatal(result, err)
	}
	after, _ := os.ReadFile(filepath.Join(service.Root, task.ID+".json"))
	if string(before) != string(after) {
		t.Fatal("API audit wrote journal")
	}
	input.ExpectedReceiptSHA256 = "different"
	raw, _ = json.Marshal(input)
	value, err = inv.handlePublicationAudit(nil, state, raw)
	if err != nil || value.(subtitlederived.PublicationAudit).Reason != "stale-record" || value.(subtitlederived.PublicationAudit).CheckedAt != "" {
		t.Fatal(value, err)
	}
	input.ExpectedReceiptSHA256 = record.ReceiptSHA256
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	value, err = inv.readPublicationAudit(ctx, state, input)
	if err != nil || value.(subtitlederived.PublicationAudit).Reason != "deadline" || value.(subtitlederived.PublicationAudit).State != "unknown" {
		t.Fatal(value, err)
	}
	if _, err := inv.handlePublicationAudit(nil, state, json.RawMessage(`{"id":"../../escape","itemIndex":0}`)); err == nil {
		t.Fatal("unsafe task ID accepted")
	}
}
func TestPublicationAuditGateIsPerInvokerAndReleasedOnAllReturns(t *testing.T) {
	inv := NewInvoker()
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := inv.withPublicationAudit(func() (any, error) { close(entered); <-release; return nil, fmt.Errorf("controlled error") })
		finished <- err
	}()
	<-entered
	if _, err := inv.withPublicationAudit(func() (any, error) { t.Error("second audit entered"); return nil, nil }); err == nil {
		t.Fatal("concurrent audit not BUSY")
	} else if app, ok := err.(*contract.AppError); !ok || app.Code != contract.CodeBusy {
		t.Fatal(err)
	}
	if _, err := NewInvoker().withPublicationAudit(func() (any, error) { return "independent", nil }); err != nil {
		t.Fatal("different Invoker shares gate", err)
	}
	close(release)
	if <-finished == nil {
		t.Fatal("controlled error lost")
	}
	if _, err := inv.withPublicationAudit(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal("error return leaked gate", err)
	}
	state := &contract.State{LibraryRoot: t.TempDir()}
	raw := json.RawMessage(`{"id":"missing","itemIndex":0}`)
	if _, err := inv.handlePublicationAudit(nil, state, raw); err == nil {
		t.Fatal("missing task accepted")
	}
	if _, err := inv.withPublicationAudit(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal("read failure leaked gate", err)
	}
}
func TestPublicationListProjectionDoesNotReadEvidenceOrClaimCurrentVerification(t *testing.T) {
	service, task, client, _, server := pointEvidenceFixture(t)
	defer server.Close()
	if _, err := service.Run(context.Background(), task.ID, pointEvidenceDependencies(client)); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(service.Root, "evidence")
	if err := os.Rename(evidence, evidence+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidence, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := NewInvoker().handleSubtitleDerivedList(&contract.State{LibraryRoot: service.Library})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range value.([]subtitlederived.Task) {
		if row.ID == task.ID {
			found = true
			if row.Items[0].PublicationRecord == nil || row.Items[0].PublicationRecord.State != "recorded" || row.Items[0].PublicationPlan != nil || row.Items[0].PublicationReceipt != nil {
				t.Fatal("projection claimed live evidence or retained refs", row)
			}
		}
	}
	if !found {
		t.Fatal("task missing")
	}
}

func TestPublicationAuditRequiresExplicitIntegralItemIndex(t *testing.T) {
	for _, raw := range []string{`{"id":"x"}`, `{"id":"x","itemIndex":null}`, `{"id":"x","itemIndex":"0"}`, `{"id":"x","itemIndex":0.5}`, `{"id":"x","itemIndex":-1}`, `{"id":"x","itemIndex":4000}`, `{"id":"x","itemIndex":0,"path":"escape"}`} {
		if _, err := NewInvoker().handlePublicationAudit(nil, &contract.State{LibraryRoot: t.TempDir()}, json.RawMessage(raw)); err == nil {
			t.Fatal("invalid audit index accepted", raw)
		}
	}
}
