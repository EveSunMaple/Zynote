package subtitlederived

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func fixtureObserver(t *testing.T) (*Service, *Task, contract.GenerationObserver, contract.GenerationAttemptEvidence) {
	t.Helper()
	service, task := recoveryTask(t)
	if err := service.bindRecipe(context.Background(), &task, Dependencies{ResolveRecipes: fixtureRecipes}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(task.Items[0].Source.Path)
	ctx, err := service.observationContext(context.Background(), &task, &task.Items[0], string(raw))
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
	event := contract.GenerationAttemptEvidence{Version: 1, Observation: metadata, State: "dispatch-intent", ConfigurationSHA256: recipeHash(task.Recipe.Configuration), EndpointSHA256: task.Recipe.Configuration.EndpointSHA256, SystemPromptSHA256: task.Items[0].Recipe.ModeRecipe.SystemPromptSHA256, RequestBodySHA256: digest("body")}
	return service, &task, observer, event
}
func TestObserverRejectsScopeConfigSequenceAndCueDrift(t *testing.T) {
	for _, bad := range []string{"scope", "source", "recipe", "config", "prompt", "cue", "chunk", "attempt", "feedback", "state"} {
		t.Run(bad, func(t *testing.T) {
			_, task, observer, event := fixtureObserver(t)
			switch bad {
			case "scope":
				event.Observation.TaskID = "../escape"
			case "source":
				event.Observation.SourceSHA256 = digest("other")
			case "recipe":
				event.Observation.ItemRecipeSHA256 = digest("other")
			case "config":
				event.ConfigurationSHA256 = digest("other")
			case "prompt":
				event.SystemPromptSHA256 = digest("other")
			case "cue":
				event.Observation.CueIDs = []int{1}
			case "chunk":
				event.Observation.ChunkIndex = 1
			case "attempt":
				event.Observation.AttemptIndex = 1
			case "feedback":
				event.Observation.FeedbackSHA256 = digest("other")
			case "state":
				event.State = "response-decoded"
			}
			if err := observer(context.Background(), event); err == nil || task.Items[0].AttemptEvidence != nil {
				t.Fatal("invalid evidence accepted", bad)
			}
		})
	}
}
func TestObserverSaveFailureLeavesOrphanWithoutRefOrAdoption(t *testing.T) {
	service, task, observer, event := fixtureObserver(t)
	blocker := filepath.Join(service.Root, task.ID+".json.tmp")
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	if err := observer(context.Background(), event); err == nil {
		t.Fatal("save failure hidden")
	}
	if task.Items[0].AttemptEvidence != nil {
		t.Fatal("failed save mutated in-memory ref")
	}
	disk, err := service.Read(task.ID)
	if err != nil || disk.Items[0].AttemptEvidence != nil {
		t.Fatal("failed save published ref", err)
	}
	files, _ := filepath.Glob(filepath.Join(service.Root, "evidence", task.ID, "0", "*.json"))
	if len(files) != 1 {
		t.Fatal("expected isolated orphan", files)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := observer(context.Background(), event); err == nil {
		t.Fatal("failed observer later adopted orphan")
	}
}
func TestObserverHeadHashAndStrictTransition(t *testing.T) {
	service, task, observer, event := fixtureObserver(t)
	if err := observer(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := observer(context.Background(), event); err == nil {
		t.Fatal("duplicate intent accepted")
	}
	// A separate observer verifies mutation of an already published head fails.
	service, task, observer, event = fixtureObserver(t)
	if err := observer(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	ref := task.Items[0].AttemptEvidence
	directory, err := evidenceDirectory(service.Root, task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(directory, "1-"+ref.HeadSHA256+".json"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	raw, _ := os.ReadFile(files[0])
	var record attemptRecord
	if json.Unmarshal(raw, &record) != nil || record.Sequence != 1 || record.PreviousSHA256 != "" {
		t.Fatal("invalid chain head")
	}
	if err := os.WriteFile(files[0], []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	event.State = "http-response-received"
	event.HTTPStatus = 200
	if err := observer(context.Background(), event); err == nil || task.Items[0].AttemptEvidence.EventCount != 1 {
		t.Fatal("mutated head accepted")
	}
}

func TestObserverRejectsEvidenceSymlinkEscape(t *testing.T) {
	service, task, observer, event := fixtureObserver(t)
	outside := t.TempDir()
	link := filepath.Join(service.Root, "evidence")
	err := os.Symlink(outside, link)
	if err != nil && runtime.GOOS == "windows" {
		command := exec.Command("powershell", "-NoProfile", "-Command", "New-Item -ItemType Junction -Path $env:ZYNOTE_LINK -Target $env:ZYNOTE_TARGET | Out-Null")
		command.Env = append(os.Environ(), "ZYNOTE_LINK="+link, "ZYNOTE_TARGET="+outside)
		err = command.Run()
	}
	if err != nil {
		t.Skip("symlink/junction unavailable", err)
	}
	if err := observer(context.Background(), event); err == nil || task.Items[0].AttemptEvidence != nil {
		t.Fatal("symlink evidence directory accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("escaped evidence root")
	}
}
func TestObserverKillHelper(t *testing.T) {
	root := os.Getenv("ZYNOTE_OBSERVER_KILL_ROOT")
	if root == "" {
		return
	}
	service := New(root, os.Getenv("ZYNOTE_OBSERVER_KILL_LIBRARY"))
	task, err := service.Read(os.Getenv("ZYNOTE_OBSERVER_KILL_ID"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(task.Items[0].Source.Path)
	ctx, err := service.observationContext(context.Background(), &task, &task.Items[0], string(body))
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
	event := contract.GenerationAttemptEvidence{Version: 1, Observation: metadata, State: "dispatch-intent", ConfigurationSHA256: recipeHash(task.Recipe.Configuration), EndpointSHA256: task.Recipe.Configuration.EndpointSHA256, SystemPromptSHA256: task.Items[0].Recipe.ModeRecipe.SystemPromptSHA256, RequestBodySHA256: digest("body")}
	if err := observer(ctx, event); err != nil {
		t.Fatal(err)
	}
	fmt.Println("observer-intent-durable")
	// Parent kills this child before it can make or complete a provider request.
	select {}
}
func TestKilledObserverIntentDoesNotReplayOrAdvanceNextMode(t *testing.T) {
	service, initial := recoveryTask(t)
	task, err := service.Create([]Source{initial.Items[0].Source}, []string{"brief", "detailed"}, initial.CourseName)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.bindRecipe(context.Background(), &task, Dependencies{ResolveRecipes: fixtureRecipes}); err != nil {
		t.Fatal(err)
	}
	task.Items[0].GenerationState = "dispatched"
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	childContext, cancelChild := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelChild()
	command := exec.CommandContext(childContext, os.Args[0], "-test.run=^TestObserverKillHelper$")
	command.Env = append(os.Environ(), "ZYNOTE_OBSERVER_KILL_ROOT="+service.Root, "ZYNOTE_OBSERVER_KILL_LIBRARY="+service.Library, "ZYNOTE_OBSERVER_KILL_ID="+task.ID)
	pipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	scanner := bufio.NewScanner(pipe)
	if !scanner.Scan() || scanner.Text() != "observer-intent-durable" {
		t.Fatal("child never persisted intent", scanner.Text())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	fresh := New(service.Root, service.Library)
	calls := 0
	result, err := fresh.Run(context.Background(), task.ID, Dependencies{ResolveRecipes: fixtureRecipes, Summarize: func(context.Context, string, string) (string, []Point, error) { calls++; return "", nil, nil }})
	if err != nil || calls != 0 || result.Items[0].AttemptEvidence == nil || result.Items[0].AttemptEvidence.EventCount != 1 || result.Items[1].GenerationState != "" {
		t.Fatal("killed intent replayed or advanced", result, calls, err)
	}
}

func TestObserverAllowsTrustedAncestorAliasButRejectsRootAlias(t *testing.T) {
	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	if err := os.MkdirAll(filepath.Join(real, "root"), 0700); err != nil {
		t.Fatal(err)
	}
	realCanonical, canonicalErr := filepath.EvalSymlinks(real)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	alias := filepath.Join(parent, "alias")
	err := os.Symlink(realCanonical, alias)
	if err != nil && runtime.GOOS == "windows" {
		command := exec.Command("powershell", "-NoProfile", "-Command", "New-Item -ItemType Junction -Path $env:ZYNOTE_LINK -Target $env:ZYNOTE_TARGET | Out-Null")
		command.Env = append(os.Environ(), "ZYNOTE_LINK="+alias, "ZYNOTE_TARGET="+realCanonical)
		err = command.Run()
	}
	if err != nil {
		t.Skip("ancestor alias unavailable", err)
	}

	directory, err := evidenceDirectory(filepath.Join(alias, "root"), "cececece-cece-4ece-8ece-cececececece", 0)
	expected, resolveErr := filepath.EvalSymlinks(filepath.Join(real, "root"))
	if err != nil || resolveErr != nil || !inside(expected, directory) {
		t.Fatal("trusted ancestor alias rejected", directory, err, resolveErr)
	}
	if _, err := evidenceDirectory(alias, "cececece-cece-4ece-8ece-cececececece", 0); err == nil {
		t.Fatal("root itself may not be alias")
	}
}
