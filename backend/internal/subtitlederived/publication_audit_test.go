package subtitlederived

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type publicationFileSnapshot struct {
	Hash     string
	Modified time.Time
	Size     int64
}

func snapshotPublicationTree(t *testing.T, roots ...string) map[string]publicationFileSnapshot {
	t.Helper()
	files := map[string]publicationFileSnapshot{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				files[path] = publicationFileSnapshot{}
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files[path] = publicationFileSnapshot{Hash: digest(string(body)), Modified: info.ModTime(), Size: info.Size()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}
func TestPublicationAuditReadsOneChainWithoutChangingTaskOrFiles(t *testing.T) {
	for _, version := range []int{0, 2} {
		t.Run(itoa(version), func(t *testing.T) {
			service, task, srt := publicationFixture(t, version)
			if service.publishSummary(context.Background(), task, 0, srt, publicationDependencies(t)) != nil {
				t.Fatal("publish fixture")
			}
			task.Items[0].Status = "失败"
			task.Items[0].Error = "must remain"
			before, _ := json.Marshal(task)
			files := snapshotPublicationTree(t, service.Root, service.Library)
			traversals := 0
			service.auditTraversalHook = func() { traversals++ }
			result := service.AuditPublication(context.Background(), *task, 0)
			if result.State != "complete" || result.CheckedAt == "" || result.CheckedFiles != 3 || result.VerifiedPoints != 2 || result.JSONPositionScope != "point-array" || traversals != 1 {
				t.Fatal(result, traversals)
			}
			if (version == 0 && result.MDPositionScope != "none") || (version == 2 && result.MDPositionScope != "anchor-appendix") {
				t.Fatal(result)
			}
			after, _ := json.Marshal(task)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(files, snapshotPublicationTree(t, service.Root, service.Library)) {
				t.Fatal("readonly audit changed journal or artifacts")
			}
		})
	}
}
func TestPublicationAuditClassifiesKnownCorruptionAndUnknownProtocols(t *testing.T) {
	for _, bad := range []string{"legacy", "pending", "plan-version", "receipt-version", "plan-hash", "receipt-hash", "receipt-without-plan", "receipt-file-missing", "source-unavailable", "source-changed", "output-changed", "policy-unsupported", "cancelled", "deadline"} {
		t.Run(bad, func(t *testing.T) {
			service, task, srt := publicationFixture(t, 2)
			if service.publishSummary(context.Background(), task, 0, srt, publicationDependencies(t)) != nil {
				t.Fatal("publish fixture")
			}
			expected, reason := "invalid", ""
			ctx := context.Background()
			switch bad {
			case "legacy":
				task.Items[0].PublicationPlan = nil
				task.Items[0].PublicationReceipt = nil
				expected = "unknown"
				reason = "no-receipt"
			case "pending":
				task.Items[0].PublicationReceipt = nil
				task.Items[0].Outputs = nil
				expected = "incomplete"
				reason = "publication-pending"
			case "plan-version":
				task.Items[0].PublicationPlan.Version = 2
				expected = "unknown"
				reason = "unsupported"
			case "receipt-version":
				task.Items[0].PublicationReceipt.Version = 2
				expected = "unknown"
				reason = "unsupported"
			case "plan-hash":
				task.Items[0].PublicationPlan.SHA256 = "broken"
				reason = "reference-invalid"
			case "receipt-hash":
				task.Items[0].PublicationReceipt.SHA256 = "broken"
				reason = "reference-invalid"
			case "receipt-without-plan":
				task.Items[0].PublicationPlan = nil
				reason = "reference-invalid"
			case "receipt-file-missing":
				directory, _ := locateEvidenceDirectory(service.Root, task.ID, 0, false)
				path, _ := publicationBlobPath(directory, "receipt", task.Items[0].PublicationReceipt.SHA256)
				_ = os.Remove(path)
			case "source-unavailable":
				_ = os.Remove(task.Items[0].Source.Path)
				expected = "unknown"
				reason = "source-unavailable"
			case "source-changed":
				_ = os.WriteFile(task.Items[0].Source.Path, []byte("changed"), 0600)
			case "output-changed":
				_ = os.WriteFile(task.Items[0].Outputs[0].Path, []byte("user edit"), 0600)
			case "policy-unsupported":
				task.Items[0].Recipe.ModeRecipe.ChunkPolicy = "future"
				task.Items[0].Recipe.Hash = itemRecipeHash(*task.Items[0].Recipe)
				expected = "unknown"
				reason = "unsupported"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				expected = "unknown"
				reason = "cancelled"
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				expected = "unknown"
				reason = "deadline"
			}
			files := snapshotPublicationTree(t, service.Root, service.Library)
			before, _ := json.Marshal(task)
			result := service.AuditPublication(ctx, *task, 0)
			if result.State != expected || (reason != "" && result.Reason != reason) || result.CheckedAt != "" || result.VerifiedPoints != 0 {
				t.Fatal(bad, result)
			}
			after, _ := json.Marshal(task)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(files, snapshotPublicationTree(t, service.Root, service.Library)) {
				t.Fatal("failure audit wrote files")
			}
		})
	}
}
func TestPublicationAuditCannotCreateRootOrResumePendingPublication(t *testing.T) {
	service, task, srt := publicationFixture(t, 2)
	service.publicationHook = func(step string) error {
		if step == "plan-durable" {
			return context.Canceled
		}
		return nil
	}
	_ = service.publishSummary(context.Background(), task, 0, srt, publicationDependencies(t))
	service.publicationHook = nil
	before := snapshotPublicationTree(t, service.Root, service.Library)
	result := service.AuditPublication(context.Background(), *task, 0)
	if result.State != "incomplete" || publicPublicationCount(t, service) != 0 || !reflect.DeepEqual(before, snapshotPublicationTree(t, service.Root, service.Library)) {
		t.Fatal("audit resumed pending plan", result)
	}
	missing := filepath.Join(t.TempDir(), "must-not-create")
	other := New(missing, service.Library)
	result = other.AuditPublication(context.Background(), *task, 0)
	if result.State != "incomplete" {
		t.Fatal(result)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatal("audit created root")
	}
}
