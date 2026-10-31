package subtitlederived

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func completeAuditFixture(t *testing.T) (*Service, *Task) {
	t.Helper()
	service, task, observer, event := fixtureObserver(t)
	raw, _ := os.ReadFile(task.Items[0].Source.Path)
	segments, _ := documents.ParseSrt(string(raw))
	event.Observation.InputSHA256, _ = summaryinput.Digest(summaryinput.New(task.Items[0].Mode, segments, 0))
	for _, state := range []string{"dispatch-intent", "http-response-received", "response-decoded", "format-result"} {
		event.State = state
		if state != "dispatch-intent" {
			event.HTTPStatus = 200
		}
		if state == "response-decoded" || state == "format-result" {
			event.DecodedTextSHA256 = digest("controlled response")
		}
		if state == "format-result" {
			valid := true
			event.FormatValid = &valid
		}
		if err := observer(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	return service, task
}
func rewriteAuditChain(t *testing.T, service *Service, task *Task, mutate func([]attemptRecord)) {
	t.Helper()
	reference := task.Items[0].AttemptEvidence
	directory, err := locateEvidenceDirectory(service.Root, task.ID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]attemptRecord, reference.EventCount)
	hash := reference.HeadSHA256
	for seq := reference.EventCount; seq >= 1; seq-- {
		record, err := readAttemptRecord(context.Background(), directory, seq, hash)
		if err != nil {
			t.Fatal(err)
		}
		records[seq-1] = record
		hash = record.PreviousSHA256
	}
	mutate(records)
	previous := ""
	for i := range records {
		// A controlled adversary can update bytes and all hash links; audit must
		// still reject forged scope/state/sequence rather than trust self-reports.
		records[i].PreviousSHA256 = previous
		body, _ := json.Marshal(records[i])
		hash := digest(string(body))
		path := filepath.Join(directory, strconv.Itoa(i+1)+"-"+hash+".json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		previous = hash
	}
	task.Items[0].AttemptEvidence = &AttemptEvidenceRef{Version: 1, EventCount: len(records), HeadSHA256: previous}
}
func TestAttemptAuditFourStatesAndNoCurrentConfiguration(t *testing.T) {
	service, legacy := recoveryTask(t)
	if result := service.AuditAttemptEvidence(context.Background(), legacy, 0); result.State != "unknown" || result.Reason != "no-observer-record" {
		t.Fatal(result)
	}
	service, task, observer, event := fixtureObserver(t)
	raw, _ := os.ReadFile(task.Items[0].Source.Path)
	segments, _ := documents.ParseSrt(string(raw))
	event.Observation.InputSHA256, _ = summaryinput.Digest(summaryinput.New(task.Items[0].Mode, segments, 0))
	if err := observer(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "incomplete" || result.CheckedEvents != 1 || result.CompleteChunks != 0 {
		t.Fatal(result)
	}
	service, task = completeAuditFixture(t)
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "complete" || result.CheckedEvents != 4 || result.TotalChunks != 1 || result.CompleteChunks != 1 {
		t.Fatal(result)
	}
	if err := os.WriteFile(task.Items[0].Source.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "invalid" || result.Reason != "source-changed" {
		t.Fatal(result)
	}
}
func TestAttemptAuditRejectsRehashedScopeRecipeAndStateForgeries(t *testing.T) {
	for _, bad := range []string{"task", "item", "source", "recipe", "config", "endpoint", "prompt", "feedback", "cue", "chunk", "attempt", "sequence", "state", "decoded", "format"} {
		t.Run(bad, func(t *testing.T) {
			service, task := completeAuditFixture(t)
			rewriteAuditChain(t, service, task, func(records []attemptRecord) {
				e := &records[0].Event
				switch bad {
				case "task":
					e.Observation.TaskID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
				case "item":
					e.Observation.ItemIndex = 1
				case "source":
					e.Observation.SourceSHA256 = digest("other")
				case "recipe":
					e.Observation.ItemRecipeSHA256 = digest("other")
				case "config":
					e.ConfigurationSHA256 = digest("other")
				case "endpoint":
					e.EndpointSHA256 = digest("other")
				case "prompt":
					e.SystemPromptSHA256 = digest("other")
				case "feedback":
					e.Observation.FeedbackSHA256 = digest("other")
				case "cue":
					e.Observation.CueIDs = []int{1}
				case "chunk":
					e.Observation.ChunkCount = 2
				case "attempt":
					e.Observation.AttemptIndex = 1
				case "sequence":
					records[0].Sequence = 2
				case "state":
					e.State = "response-decoded"
				case "decoded":
					records[2].Event.DecodedTextSHA256 = "not-a-digest"
				case "format":
					records[3].Event.FormatValid = nil
				}
			})
			if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "invalid" {
				t.Fatal("forgery accepted", bad, result)
			}
		})
	}
}
func TestAttemptAuditRejectsReferenceAndRecipeCorruption(t *testing.T) {
	for _, bad := range []string{"count-high", "count-low", "count-cap", "head", "task-hash", "task-config", "item-hash", "item-mode", "item-source", "item-task", "version", "missing-file", "modified-file", "previous-cycle"} {
		t.Run(bad, func(t *testing.T) {
			service, task := completeAuditFixture(t)
			reference := task.Items[0].AttemptEvidence
			switch bad {
			case "count-high":
				reference.EventCount++
			case "count-low":
				reference.EventCount--
			case "count-cap":
				reference.EventCount = 160001
			case "head":
				reference.HeadSHA256 = digest("missing")
			case "task-hash":
				task.Recipe.Hash = digest("wrong")
			case "task-config":
				task.Recipe.Configuration.RequestedModel = "changed"
			case "item-hash":
				task.Items[0].Recipe.Hash = digest("wrong")
			case "item-mode":
				task.Items[0].Mode = "detailed"
			case "item-source":
				task.Items[0].Recipe.SourceSHA256 = digest("wrong")
				task.Items[0].Recipe.Hash = itemRecipeHash(*task.Items[0].Recipe)
			case "item-task":
				task.Items[0].Recipe.TaskRecipeHash = digest("wrong")
				task.Items[0].Recipe.Hash = itemRecipeHash(*task.Items[0].Recipe)
			case "version":
				reference.Version = 99
			case "missing-file", "modified-file":
				directory, _ := locateEvidenceDirectory(service.Root, task.ID, 0, false)
				path := filepath.Join(directory, "4-"+reference.HeadSHA256+".json")
				if bad == "missing-file" {
					_ = os.Remove(path)
				} else {
					_ = os.WriteFile(path, []byte("modified"), 0600)
				}
			case "previous-cycle":
				directory, _ := locateEvidenceDirectory(service.Root, task.ID, 0, false)
				record, _ := readAttemptRecord(context.Background(), directory, reference.EventCount, reference.HeadSHA256)
				record.PreviousSHA256 = reference.HeadSHA256
				body, _ := json.Marshal(record)
				hash := digest(string(body))
				_ = os.WriteFile(filepath.Join(directory, "4-"+hash+".json"), body, 0600)
				reference.HeadSHA256 = hash
			}
			if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "invalid" {
				t.Fatal("corruption accepted", bad, result)
			}
		})
	}
}
func TestAttemptAuditUnavailableSourceIsUnknownAndCreatesNothing(t *testing.T) {
	service, task := completeAuditFixture(t)
	before, _ := os.ReadFile(filepath.Join(service.Root, task.ID+".json"))
	if err := os.Remove(task.Items[0].Source.Path); err != nil {
		t.Fatal(err)
	}
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "unknown" || result.Reason != "source-unverifiable" {
		t.Fatal(result)
	}
	after, _ := os.ReadFile(filepath.Join(service.Root, task.ID+".json"))
	if string(before) != string(after) {
		t.Fatal("audit changed task")
	}
	// A fake ref with an absent evidence directory must not create it.
	service, task = completeAuditFixture(t)
	service.Root = filepath.Join(t.TempDir(), "never-created")
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "invalid" {
		t.Fatal(result)
	}
	if _, err := os.Stat(service.Root); !os.IsNotExist(err) {
		t.Fatal("read-only audit created evidence root", err)
	}
}
func TestAttemptAuditSourceEscapeAndEvidenceDirectoryAliasRejected(t *testing.T) {
	service, task := completeAuditFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.srt")
	body, _ := os.ReadFile(task.Items[0].Source.Path)
	if err := os.WriteFile(outside, body, 0600); err != nil {
		t.Fatal(err)
	}
	task.Items[0].Source.Path = outside
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "invalid" || result.Reason != "source-path-invalid" {
		t.Fatal(result)
	}
	service, task = completeAuditFixture(t)
	directory, _ := locateEvidenceDirectory(service.Root, task.ID, 0, false)
	if err := os.Rename(directory, directory+"-moved"); err != nil {
		t.Fatal(err)
	}
	linkErr := os.Symlink(directory+"-moved", directory)
	if linkErr != nil && runtime.GOOS == "windows" {
		command := exec.Command("powershell", "-NoProfile", "-Command", "New-Item -ItemType Junction -Path $env:ZYNOTE_LINK -Target $env:ZYNOTE_TARGET | Out-Null")
		command.Env = append(os.Environ(), "ZYNOTE_LINK="+directory, "ZYNOTE_TARGET="+directory+"-moved")
		linkErr = command.Run()
	}
	if linkErr != nil {
		t.Skip("directory alias unavailable", linkErr)
	}
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "invalid" || result.Reason != "evidence-path-invalid" {
		t.Fatal(result)
	}
}

func TestAttemptAuditRejectsRehashedActualInputMismatch(t *testing.T) {
	for _, change := range []string{"mode", "text", "time", "digest"} {
		t.Run(change, func(t *testing.T) {
			service, task := completeAuditFixture(t)
			raw, _ := os.ReadFile(task.Items[0].Source.Path)
			segments, _ := documents.ParseSrt(string(raw))
			input := summaryinput.New(task.Items[0].Mode, segments, 0)
			switch change {
			case "mode":
				input.Mode = "detailed"
			case "text":
				input.Cues[0].Text = "forged text"
			case "time":
				input.Cues[0].Time = "00:01:00"
			}
			hash, _ := summaryinput.Digest(input)
			if change == "digest" {
				hash = digest("other input")
			}
			rewriteAuditChain(t, service, task, func(records []attemptRecord) {
				for i := range records {
					records[i].Event.Observation.InputSHA256 = hash
				}
			})
			if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "invalid" || result.Reason != "input-contract-invalid" {
				t.Fatal("rehashed input forgery accepted", change, result)
			}
		})
	}
}
func auditFileSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[path] = "directory"
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[path] = digest(string(body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestAttemptAuditReadOnlyCancellationAndUnsupportedPolicy(t *testing.T) {
	service, task := completeAuditFixture(t)
	before := auditFileSnapshot(t, service.Root)
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "complete" {
		t.Fatal(result)
	}
	if after := auditFileSnapshot(t, service.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("audit wrote or created files")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := service.AuditAttemptEvidence(ctx, *task, 0); result.State != "unknown" || result.Reason != "cancelled" {
		t.Fatal(result)
	}
	task.Items[0].Recipe.ModeRecipe.ChunkPolicy = "future-policy"
	task.Items[0].Recipe.Hash = itemRecipeHash(*task.Items[0].Recipe)
	if result := service.AuditAttemptEvidence(context.Background(), *task, 0); result.State != "unknown" || result.Reason != "policy-unsupported" {
		t.Fatal("future policy falsely audited", result)
	}
}

func TestAttemptAuditRejectsContinuousButWrongFrozenPartition(t *testing.T) {
	service, task := recoveryTask(t)
	raw := "1\n00:00:01,000 --> 00:00:02,000\nfirst\n\n2\n00:00:02,000 --> 00:00:03,000\nsecond\n"
	if err := os.WriteFile(task.Items[0].Source.Path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	task.Items[0].Source.Hash = digest(raw)
	if err := service.bindRecipe(context.Background(), &task, Dependencies{ResolveRecipes: fixtureRecipes}); err != nil {
		t.Fatal(err)
	}
	task.Items[0].Recipe.ModeRecipe.ChunkLimit = 1
	task.Items[0].Recipe.Hash = itemRecipeHash(*task.Items[0].Recipe)
	ctx, err := service.observationContext(context.Background(), &task, &task.Items[0], raw)
	if err != nil {
		t.Fatal(err)
	}
	metadata, observer := contract.GenerationObservationFrom(ctx)
	segments, _ := documents.ParseSrt(raw)
	metadata.ChunkIndex = 0
	metadata.ChunkCount = 1
	metadata.AttemptIndex = 0
	metadata.CueIDs = []int{0, 1}
	metadata.FeedbackSHA256 = digest("")
	metadata.InputSHA256, _ = summaryinput.Digest(summaryinput.New("brief", segments, 0))
	event := contract.GenerationAttemptEvidence{Version: 1, Observation: metadata, ConfigurationSHA256: recipeHash(task.Recipe.Configuration), EndpointSHA256: task.Recipe.Configuration.EndpointSHA256, SystemPromptSHA256: task.Items[0].Recipe.ModeRecipe.SystemPromptSHA256, RequestBodySHA256: digest("body")}
	for _, state := range []string{"dispatch-intent", "http-response-received", "response-decoded", "format-result"} {
		event.State = state
		if state != "dispatch-intent" {
			event.HTTPStatus = 200
		}
		if state == "response-decoded" || state == "format-result" {
			event.DecodedTextSHA256 = digest("reply")
		}
		if state == "format-result" {
			valid := true
			event.FormatValid = &valid
		}
		if err := observer(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if result := service.AuditAttemptEvidence(context.Background(), task, 0); result.State != "invalid" || result.Reason != "input-contract-invalid" {
		t.Fatal("wrong frozen partition accepted", result)
	}
}

type midAuditCancellation struct {
	context.Context
	remaining int
	cancel    context.CancelFunc
}

func (value *midAuditCancellation) Err() error {
	value.remaining--
	if value.remaining == 0 {
		value.cancel()
	}
	return value.Context.Err()
}
func TestAttemptAuditCancellationDuringChainWalkDoesNotWrite(t *testing.T) {
	service, task := completeAuditFixture(t)
	before := auditFileSnapshot(t, service.Root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlled := &midAuditCancellation{Context: ctx, remaining: 8, cancel: cancel}
	if result := service.AuditAttemptEvidence(controlled, *task, 0); result.State != "unknown" || result.Reason != "cancelled" || controlled.remaining > 0 {
		t.Fatal("chain walk ignored cancellation", result, controlled.remaining)
	}
	if after := auditFileSnapshot(t, service.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("cancelled audit wrote files")
	}
}
