package subtitlederived

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func publicationFixture(t *testing.T, version int) (*Service, *Task, string) {
	t.Helper()
	service, task := recoveryTask(t)
	task.OutputVersion = version
	task.Items[0].Mode = "mindmap"
	if err := service.bindRecipe(context.Background(), &task, Dependencies{ResolveRecipes: fixtureRecipes}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(task.Items[0].Source.Path)
	srt := string(raw)
	ctx, err := service.observationContext(context.Background(), &task, &task.Items[0], srt)
	if err != nil {
		t.Fatal(err)
	}
	meta, observer := contract.GenerationObservationFrom(ctx)
	segments, _ := documents.ParseSrt(srt)
	meta.ChunkIndex = 0
	meta.ChunkCount = 1
	meta.AttemptIndex = 0
	meta.CueIDs = []int{0}
	meta.FeedbackSHA256 = digest("")
	meta.InputSHA256, _ = summaryinput.Digest(summaryinput.New("mindmap", segments, 0))
	event := contract.GenerationAttemptEvidence{Version: 2, Observation: meta, ConfigurationSHA256: recipeHash(task.Recipe.Configuration), EndpointSHA256: task.Recipe.Configuration.EndpointSHA256, SystemPromptSHA256: task.Items[0].Recipe.ModeRecipe.SystemPromptSHA256, RequestBodySHA256: digest("controlled body")}
	points := []Point{{Cue: 0, Quote: "第一句", Point: "中文要点一", Timestamp: "00:00:01"}, {Cue: 0, Quote: "第一句", Point: "中文要点二", Timestamp: "00:00:01"}}
	rows, _ := canonicalDerivedPoints(points)
	hash, _ := contract.SummaryPointsDigest(rows)
	for _, state := range []string{"dispatch-intent", "http-response-received", "response-decoded", "format-result", "validated-points"} {
		event.State = state
		if state != "dispatch-intent" {
			event.HTTPStatus = 200
		}
		if state == "response-decoded" || state == "format-result" || state == "validated-points" {
			event.DecodedTextSHA256 = digest("controlled decoded")
		}
		if state == "format-result" || state == "validated-points" {
			value := true
			event.FormatValid = &value
		}
		if state == "validated-points" {
			receipt, err := contract.ReadGenerationReceipt(ctx)
			if err != nil {
				t.Fatal(err)
			}
			event.ValidatedPoints = &contract.ValidatedPointEvidence{AcceptedFormatRef: receipt.Ref, PointCount: len(points), PointsSHA256: hash}
		}
		if err := observer(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	receipt, _ := contract.ReadGenerationReceipt(ctx)
	facts := &contract.SummaryEvidence{Version: 1, SourceSHA256: task.Items[0].Source.Hash, ItemRecipeHash: task.Items[0].Recipe.Hash, FinalPointsSHA256: hash, PointCount: len(points), ValidatedHead: receipt.Ref}
	if err := service.saveReceived(ctx, &task, &task.Items[0], SummaryResult{Markdown: "# 中文摘要\n\n正文保留\n", Points: points, Facts: facts}); err != nil {
		t.Fatal(err)
	}
	return service, &task, srt
}
func publicationDependencies(t *testing.T) Dependencies {
	t.Helper()
	return Dependencies{ResolveRecipes: func(context.Context, []string) (contract.RequestedGenerationConfig, map[string]ModeRecipe, error) {
		t.Error("profile consulted")
		return contract.RequestedGenerationConfig{}, nil, fmt.Errorf("forbidden")
	}, Summarize: func(context.Context, string, string) (string, []Point, error) {
		t.Error("provider replayed")
		return "", nil, fmt.Errorf("forbidden")
	}, RenderSVG: func(string, []Point) string { return "<svg>受控</svg>" }}
}
func publicPublicationCount(t *testing.T, service *Service) int {
	t.Helper()
	count := 0
	_ = filepath.WalkDir(filepath.Join(service.Library, "Notes"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".note-") {
			count++
		}
		return nil
	})
	return count
}
func TestPublicationPreservesExactBytesAndLocationsForBothOutputVersions(t *testing.T) {
	for _, version := range []int{0, 2} {
		t.Run(itoa(version), func(t *testing.T) {
			service, task, srt := publicationFixture(t, version)
			original := *task.Items[0].Prepared
			pointer := &task.Items[0]
			deps := publicationDependencies(t)
			if err := service.publishSummary(context.Background(), task, 0, srt, deps); err != nil {
				t.Fatal(err)
			}
			if pointer != &task.Items[0] || pointer.Prepared != nil || pointer.PublicationReceipt == nil || len(pointer.Outputs) != 3 {
				t.Fatal("unstable commit", pointer)
			}
			plan, directory, err := service.loadPublication(context.Background(), task, 0)
			if err != nil {
				t.Fatal(err)
			}
			bodies, err := service.publicationFiles(context.Background(), plan, directory, true)
			if err != nil {
				t.Fatal(err)
			}
			points := original.Points
			appendix := ""
			if version == 2 {
				points, appendix, err = anchorPoints(srt, points)
				if err != nil {
					t.Fatal(err)
				}
			}
			provenance := "\n\n---\n字幕来源：" + pointer.Source.URL + "\n来源版本：" + pointer.Source.Generation + "\n字幕 SHA256：" + pointer.Source.Hash + "\n"
			if string(bodies[0]) != original.Markdown+appendix+provenance || string(bodies[1]) != prettyJSON(points) || string(bodies[2]) != "<svg>受控</svg>" {
				t.Fatal("output bytes changed")
			}
			if validatePublicationRows(context.Background(), plan, bodies, srt) != nil {
				t.Fatal("locations invalid")
			}
			if version == 0 {
				for _, row := range plan.Points {
					if row.MD.State != "not-present" {
						t.Fatal(row)
					}
				}
			} else if plan.Points[0].MD.ByteOffset >= plan.Points[1].MD.ByteOffset || plan.Points[0].MD.SHA256 != plan.Points[1].MD.SHA256 {
				t.Fatal("duplicate cues lost independent offsets")
			}
			deps.RenderSVG = func(string, []Point) string { t.Fatal("recovery renderer called"); return "" }
			if err := service.restorePublication(context.Background(), task, 0, srt); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPublicationFailureBoundariesKeepPublicFilesAndRecoverLocally(t *testing.T) {
	stages := []string{"blob-before", "plan-before", "plan-save-before", "plan-durable", "file-before-md", "file-durable-md", "file-durable-json", "file-durable-svg", "receipt-before", "receipt-durable", "commit-before", "commit-durable"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			service, task, srt := publicationFixture(t, 2)
			deps := publicationDependencies(t)
			pointer := &task.Items[0]
			service.publicationHook = func(step string) error {
				if step == stage {
					return fmt.Errorf("controlled failure")
				}
				return nil
			}
			if err := service.publishSummary(context.Background(), task, 0, srt, deps); !contract.IsObservationFailure(err) {
				t.Fatal("failure not fatal", err)
			}
			if pointer != &task.Items[0] {
				t.Fatal("pointer replaced")
			}
			count := publicPublicationCount(t, service)
			expected := 0
			switch stage {
			case "file-durable-md":
				expected = 1
			case "file-durable-json":
				expected = 2
			case "file-durable-svg", "receipt-before", "receipt-durable", "commit-before", "commit-durable":
				expected = 3
			}
			if count != expected {
				t.Fatal("public files lost", count, expected)
			}
			disk, err := service.Read(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stage != "commit-durable" && (disk.Items[0].Prepared == nil || disk.Items[0].PublicationReceipt != nil || len(disk.Items[0].Outputs) != 0) {
				t.Fatal("uncommitted success", disk.Items[0])
			}
			service.publicationHook = nil
			if disk.Items[0].PublicationPlan != nil {
				deps.RenderSVG = func(string, []Point) string { t.Fatal("planned recovery rerendered"); return "" }
			}
			result, err := service.Run(context.Background(), task.ID, deps)
			if err != nil || result.Items[0].Status != "已完成" || publicPublicationCount(t, service) != 3 {
				t.Fatal("local recovery failed", result, err)
			}
		})
	}
}
func TestPublicationRealTaskSaveFailuresDoNotFakePlanOrReceipt(t *testing.T) {
	for _, stage := range []string{"plan-save-before", "commit-before"} {
		t.Run(stage, func(t *testing.T) {
			service, task, srt := publicationFixture(t, 2)
			blocker := filepath.Join(service.Root, task.ID+".json.tmp")
			service.publicationHook = func(step string) error {
				if step == stage {
					return os.Mkdir(blocker, 0700)
				}
				return nil
			}
			if err := service.publishSummary(context.Background(), task, 0, srt, publicationDependencies(t)); !contract.IsObservationFailure(err) {
				t.Fatal(err)
			}
			disk, _ := service.Read(task.ID)
			if disk.Items[0].PublicationReceipt != nil || task.Items[0].PublicationReceipt != nil || task.Items[0].Prepared == nil {
				t.Fatal("save failure faked commit")
			}
			if stage == "plan-save-before" && (disk.Items[0].PublicationPlan != nil || task.Items[0].PublicationPlan != nil || publicPublicationCount(t, service) != 0) {
				t.Fatal("plan save failure published")
			}
			if stage == "commit-before" && publicPublicationCount(t, service) != 3 {
				t.Fatal("commit failure removed public files")
			}
			if err := os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			service.publicationHook = nil
			if _, err := service.Run(context.Background(), task.ID, publicationDependencies(t)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPublicationCommittedReceiptNeverRecreatedAndPublicConflictNeverOverwritten(t *testing.T) {
	for _, bad := range []string{"receipt-missing", "output-changed", "source-changed", "span", "index", "version", "scope", "blob-missing"} {
		t.Run(bad, func(t *testing.T) {
			service, task, srt := publicationFixture(t, 2)
			deps := publicationDependencies(t)
			service.publicationHook = func(step string) error {
				if step == "plan-durable" {
					return fmt.Errorf("stop")
				}
				return nil
			}
			_ = service.publishSummary(context.Background(), task, 0, srt, deps)
			service.publicationHook = nil
			plan, directory, err := service.loadPublication(context.Background(), task, 0)
			if err != nil {
				t.Fatal(err)
			}
			if bad == "receipt-missing" || bad == "output-changed" || bad == "source-changed" {
				if err := service.publishSummary(context.Background(), task, 0, srt, deps); err != nil {
					t.Fatal(err)
				}
				if bad == "receipt-missing" {
					path, _ := publicationBlobPath(directory, "receipt", task.Items[0].PublicationReceipt.SHA256)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if bad == "output-changed" {
					if err := os.WriteFile(task.Items[0].Outputs[0].Path, []byte("user edit"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if bad == "source-changed" {
					srt = "changed source"
				}
				before, _ := json.Marshal(task)
				if service.restorePublication(context.Background(), task, 0, srt) == nil {
					t.Fatal("bad receipt accepted")
				}
				after, _ := json.Marshal(task)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("read verification mutated task")
				}
				return
			}
			switch bad {
			case "span":
				plan.Points[0].MD.ByteOffset++
			case "index":
				plan.Points[0].ArrayIndex = 1
			case "version":
				plan.Version = 2
			case "scope":
				plan.ItemIndex = 1
			case "blob-missing":
				path, _ := publicationBlobPath(directory, "blob", plan.Files[0].SHA256)
				_ = os.Remove(path)
			}
			if bad != "blob-missing" {
				body, _ := json.Marshal(plan)
				hash, err := publishPublicationBytes(context.Background(), directory, "plan", body, publicationLimit)
				if err != nil {
					t.Fatal(err)
				}
				task.Items[0].PublicationPlan.SHA256 = hash
			}
			if service.publishSummary(context.Background(), task, 0, srt, deps) == nil || publicPublicationCount(t, service) != 0 {
				t.Fatal("tampered plan published")
			}
		})
	}
}
func TestPublicationStreamLimitCancellationAndNoOverwrite(t *testing.T) {
	service, task, srt := publicationFixture(t, 2)
	deps := publicationDependencies(t)
	service.publicationHook = func(step string) error {
		if step == "plan-durable" {
			return fmt.Errorf("stop")
		}
		return nil
	}
	_ = service.publishSummary(context.Background(), task, 0, srt, deps)
	service.publicationHook = nil
	plan, _, err := service.loadPublication(context.Background(), task, 0)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := service.publicationPath(plan.Files[0].Path)
	if err := os.WriteFile(path, []byte("existing user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if service.publishSummary(context.Background(), task, 0, srt, deps) == nil {
		t.Fatal("conflict overwritten")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "existing user file" {
		t.Fatal("user file changed")
	}
	if _, err := readPublicationBytes(context.Background(), path, digest(string(body)), 3); err == nil {
		t.Fatal("stream cap ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readPublicationBytes(ctx, path, digest(string(body)), publicationLimit); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestPublicationSidecarWriteFailuresAndOversizeStopBeforeCommit(t *testing.T) {
	for _, kind := range []string{"blob", "plan", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			service, task, srt := publicationFixture(t, 2)
			deps := publicationDependencies(t)
			plan, bodies, err := service.buildPublication(context.Background(), task, 0, srt, deps)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := evidenceDirectory(service.Root, task.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			var hash string
			switch kind {
			case "blob":
				hash = digest(string(bodies[0]))
			case "plan":
				body, _ := json.Marshal(plan)
				hash = digest(string(body))
			case "receipt":
				service.publicationHook = func(step string) error {
					if step == "plan-durable" {
						return fmt.Errorf("stop")
					}
					return nil
				}
				_ = service.publishSummary(context.Background(), task, 0, srt, deps)
				service.publicationHook = nil
				receipt := publicationReceipt{Version: 1, TaskID: task.ID, ItemIndex: 0, PlanSHA256: task.Items[0].PublicationPlan.SHA256, Evidence: plan.Evidence, Files: plan.Files, PointCount: len(plan.Points), MDPositionScope: plan.MDPositionScope}
				body, _ := json.Marshal(receipt)
				hash = digest(string(body))
			}
			path, _ := publicationBlobPath(directory, kind, hash)
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if service.publishSummary(context.Background(), task, 0, srt, deps) == nil || task.Items[0].PublicationReceipt != nil || task.Items[0].Prepared == nil {
				t.Fatal("sidecar failure committed")
			}
			count := publicPublicationCount(t, service)
			if (kind == "receipt" && count != 3) || (kind != "receipt" && count != 0) {
				t.Fatal("wrong retained files", count)
			}
		})
	}
	service, task, srt := publicationFixture(t, 2)
	task.Items[0].Prepared.Markdown = strings.Repeat("x", publicationLimit+1)
	if service.publishSummary(context.Background(), task, 0, srt, publicationDependencies(t)) == nil || publicPublicationCount(t, service) != 0 || task.Items[0].PublicationPlan != nil {
		t.Fatal("oversize published")
	}
}

func TestPublicationRejectsOutputDirectoryAlias(t *testing.T) {
	service, task, srt := publicationFixture(t, 2)
	deps := publicationDependencies(t)
	service.publicationHook = func(step string) error {
		if step == "plan-durable" {
			return fmt.Errorf("stop")
		}
		return nil
	}
	_ = service.publishSummary(context.Background(), task, 0, srt, deps)
	service.publicationHook = nil
	plan, _, err := service.loadPublication(context.Background(), task, 0)
	if err != nil {
		t.Fatal(err)
	}
	path, err := service.publicationPath(plan.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(path)
	moved := directory + "-moved"
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	linkErr := os.Symlink(moved, directory)
	if linkErr != nil && runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-NoProfile", "-Command", "New-Item -ItemType Junction -Path $env:ZYNOTE_LINK -Target $env:ZYNOTE_TARGET | Out-Null")
		cmd.Env = append(os.Environ(), "ZYNOTE_LINK="+directory, "ZYNOTE_TARGET="+moved)
		linkErr = cmd.Run()
	}
	if linkErr != nil {
		t.Fatal("cannot exercise alias rejection", linkErr)
	}
	if service.publishSummary(context.Background(), task, 0, srt, deps) == nil {
		t.Fatal("directory alias accepted")
	}
}

func TestPublicationNotesAliasCannotCreateCourseOutsideLibrary(t *testing.T) {
	service, task, srt := publicationFixture(t, 2)
	outside := t.TempDir()
	notes := filepath.Join(service.Library, "Notes")
	linkErr := os.Symlink(outside, notes)
	if linkErr != nil && runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-NoProfile", "-Command", "New-Item -ItemType Junction -Path $env:ZYNOTE_LINK -Target $env:ZYNOTE_TARGET | Out-Null")
		cmd.Env = append(os.Environ(), "ZYNOTE_LINK="+notes, "ZYNOTE_TARGET="+outside)
		linkErr = cmd.Run()
	}
	if linkErr != nil {
		t.Fatal("cannot exercise Notes alias", linkErr)
	}
	if task.CourseName == "" {
		t.Fatal("fixture needs nonempty course")
	}
	if service.publishSummary(context.Background(), task, 0, srt, publicationDependencies(t)) == nil {
		t.Fatal("Notes alias accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("alias caused external directory or file creation", entries, err)
	}
}

func TestPublicationFailureStopsLaterModeAndKeepsVisibleFile(t *testing.T) {
	for _, failure := range []string{"file-durable-md", "source-after-md"} {
		t.Run(failure, func(t *testing.T) {
			service, task, _ := publicationFixture(t, 2)
			task.Items = append(task.Items, Item{Source: task.Items[0].Source, Mode: "brief", Status: "等待处理"})
			if service.save(*task) != nil {
				t.Fatal("save fixture")
			}
			service.publicationHook = func(step string) error {
				if step == "file-durable-md" {
					if failure == "source-after-md" {
						return os.WriteFile(task.Items[0].Source.Path, []byte("changed"), 0600)
					}
					return fmt.Errorf("controlled stop")
				}
				return nil
			}
			result, err := service.Run(context.Background(), task.ID, publicationDependencies(t))
			if !contract.IsObservationFailure(err) || result.Items[1].GenerationState != "" || result.Items[1].Status != "等待处理" || publicPublicationCount(t, service) < 1 || result.Items[0].Prepared == nil || result.Items[0].PublicationReceipt != nil {
				t.Fatal("publication failure continued or cleaned", result, err)
			}
		})
	}
}

type publicationCancelAfterChecks struct {
	context.Context
	remaining int
}

func (value *publicationCancelAfterChecks) Err() error {
	value.remaining--
	if value.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
func TestPublicationMidValidationAndPublicationCancellation(t *testing.T) {
	service, task, srt := publicationFixture(t, 2)
	deps := publicationDependencies(t)
	plan, bodies, err := service.buildPublication(context.Background(), task, 0, srt, deps)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &publicationCancelAfterChecks{Context: context.Background(), remaining: 7}
	if validatePublicationRows(ctx, plan, bodies, srt) == nil || publicPublicationCount(t, service) != 0 {
		t.Fatal("mid-validation cancel ignored")
	}
	for _, stage := range []string{"plan-durable", "file-durable-md"} {
		t.Run(stage, func(t *testing.T) {
			service, task, srt := publicationFixture(t, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service.publicationHook = func(step string) error {
				if step == stage {
					cancel()
				}
				return nil
			}
			if !contract.IsObservationFailure(service.publishSummary(ctx, task, 0, srt, publicationDependencies(t))) || task.Items[0].PublicationReceipt != nil || task.Items[0].Prepared == nil {
				t.Fatal("cancel committed publication")
			}
			expected := 0
			if stage == "file-durable-md" {
				expected = 1
			}
			if publicPublicationCount(t, service) != expected {
				t.Fatal("cancel removed published files or continued")
			}
		})
	}
}
func TestPublicationKillHelper(t *testing.T) {
	if os.Getenv("ZYNOTE_PUBLICATION_KILL") != "1" {
		return
	}
	service := New(os.Getenv("ZYNOTE_PUBLICATION_ROOT"), os.Getenv("ZYNOTE_PUBLICATION_LIBRARY"))
	stage := os.Getenv("ZYNOTE_PUBLICATION_STAGE")
	service.publicationHook = func(step string) error {
		if step == stage {
			fmt.Println("PUBLICATION_READY")
			for {
				time.Sleep(time.Second)
			}
		}
		return nil
	}
	_, _ = service.Run(context.Background(), os.Getenv("ZYNOTE_PUBLICATION_TASK"), Dependencies{RenderSVG: func(string, []Point) string { return "<svg>受控</svg>" }})
	t.Fatal("publication kill checkpoint not reached")
}
func TestPublicationSubprocessKillsRecoverOnlyLocally(t *testing.T) {
	for _, stage := range []string{"plan-durable", "file-durable-md", "file-durable-svg", "receipt-durable", "commit-durable"} {
		t.Run(stage, func(t *testing.T) {
			service, task, _ := publicationFixture(t, 2)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPublicationKillHelper$")
			cmd.Env = append(os.Environ(), "ZYNOTE_PUBLICATION_KILL=1", "ZYNOTE_PUBLICATION_ROOT="+service.Root, "ZYNOTE_PUBLICATION_LIBRARY="+service.Library, "ZYNOTE_PUBLICATION_TASK="+task.ID, "ZYNOTE_PUBLICATION_STAGE="+stage)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(stdout)
			ready := false
			for scanner.Scan() {
				if scanner.Text() == "PUBLICATION_READY" {
					ready = true
					break
				}
			}
			if !ready || ctx.Err() != nil {
				_ = cmd.Wait()
				t.Fatalf("kill point not reached: %v %s", ctx.Err(), stderr.String())
			}
			if err := cmd.Process.Kill(); err != nil {
				_ = cmd.Wait()
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil || ctx.Err() != nil {
				t.Fatal("parent kill not observed", err, ctx.Err())
			}
			deps := publicationDependencies(t)
			deps.RenderSVG = func(string, []Point) string { t.Fatal("kill recovery rerendered"); return "" }
			result, err := New(service.Root, service.Library).Run(context.Background(), task.ID, deps)
			if err != nil || result.Items[0].Status != "已完成" || result.Items[0].PublicationReceipt == nil {
				t.Fatal(result, err)
			}
		})
	}
}
