package subtitlederived

import (
	"context"
	"fmt"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type summaryStageFactory struct {
	service *Service
	task    *Task
	item    *Item
	index   int
}

func (factory *summaryStageFactory) SaveArtifact(ctx context.Context, body []byte) (string, error) {
	return (treeStorage{factory.service, *factory.task, factory.index}).Put(ctx, body)
}

func newSummaryStageFactory(service *Service, task *Task, item *Item) contract.SummaryStageFactory {
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	return &summaryStageFactory{service, task, item, index}
}
func stageIdentity(purpose string, index int, input, prompt, source, recipe string) string {
	return recipeHash(struct {
		Purpose                       string
		Index                         int
		Input, Prompt, Source, Recipe string
	}{purpose, index, input, prompt, source, recipe})
}
func validStageTransition(previous *contract.GenerationAttemptEvidence, event contract.GenerationAttemptEvidence, recipe *ItemRecipe) bool {
	m := event.Observation
	if (event.State == statusFormatResult && !recipeHashPattern.MatchString(event.JSONBodySHA256)) || (event.State != statusFormatResult && event.JSONBodySHA256 != "") {
		return false
	}
	expectedPrompt := ""
	switch m.Purpose {
	case "summary-reduce":
		expectedPrompt = recipe.ModeRecipe.ReducePromptSHA256
	case "summary-verify":
		expectedPrompt = recipe.ModeRecipe.VerifyPromptSHA256
	default:
		return false
	}
	if recipe.ModeRecipe.Version != 3 || event.Version != 4 || m.ProtocolVersion != 4 || m.ChunkIndex < 0 || m.ChunkIndex >= 8192 || m.ChunkCount != 1 || len(m.CueIDs) != 0 || len(m.CoreSpans) != 0 || event.SystemPromptSHA256 != expectedPrompt || m.StageID != stageIdentity(m.Purpose, m.ChunkIndex, m.InputSHA256, event.SystemPromptSHA256, m.SourceSHA256, m.ItemRecipeSHA256) {
		return false
	}
	convert := func(input contract.GenerationAttemptEvidence) contract.GenerationAttemptEvidence {
		input = contract.CloneGenerationEvent(input)
		input.Version = 2
		input.Observation.ProtocolVersion = 2
		input.Observation.Purpose = "subtitle-summary"
		input.Observation.ChunkIndex = 0
		input.Observation.CueIDs = []int{0}
		return input
	}
	converted := convert(event)
	var prior *contract.GenerationAttemptEvidence
	if previous != nil {
		if previous.Observation.StageID != m.StageID {
			return false
		}
		value := convert(*previous)
		prior = &value
	}
	copy := *recipe
	copy.ModeRecipe = recipe.ModeRecipe
	copy.ModeRecipe.SystemPromptSHA256 = expectedPrompt
	return validAttemptTransition(prior, converted, 1, &copy)
}
func (service *Service) stageReceipt(ctx context.Context, task Task, index int, key string) (contract.GenerationReceipt, error) {
	if index < 0 || index >= len(task.Items) || task.Recipe == nil || task.Items[index].Recipe == nil {
		return contract.GenerationReceipt{}, observerError()
	}
	reference := task.Items[index].Stages[key]
	if reference == nil || reference.Version != 4 || reference.EventCount < 1 || reference.EventCount > 16 {
		return contract.GenerationReceipt{}, observerError()
	}
	directory, err := locateEvidenceDirectory(service.Root, task.ID, index, false)
	if err != nil {
		return contract.GenerationReceipt{}, err
	}
	records := make([]attemptRecord, reference.EventCount)
	hashes := make([]string, reference.EventCount)
	hash := reference.HeadSHA256
	for sequence := reference.EventCount; sequence >= 1; sequence-- {
		record, err := readAttemptRecord(ctx, directory, sequence, hash)
		if err != nil {
			return contract.GenerationReceipt{}, err
		}
		if record.Version != 4 || record.Event.Version != 4 {
			return contract.GenerationReceipt{}, observerError()
		}
		records[sequence-1] = record
		hashes[sequence-1] = hash
		hash = record.PreviousSHA256
	}
	if hash != "" {
		return contract.GenerationReceipt{}, observerError()
	}
	var previous *contract.GenerationAttemptEvidence
	item := task.Items[index]
	for i, record := range records {
		event := record.Event
		m := event.Observation
		if m.StageID != key || m.TaskID != task.ID || m.ItemIndex != index || m.ItemRecipeSHA256 != item.Recipe.Hash || m.SourceSHA256 != item.Source.Hash || event.ConfigurationSHA256 != recipeHash(task.Recipe.Configuration) || event.EndpointSHA256 != task.Recipe.Configuration.EndpointSHA256 || !validStageTransition(previous, event, item.Recipe) || (i > 0 && record.PreviousSHA256 != hashes[i-1]) {
			return contract.GenerationReceipt{}, observerError()
		}
		previous = &event
	}
	result := contract.GenerationReceipt{Ref: contract.GenerationEventRef{Version: 4, Sequence: reference.EventCount, SHA256: reference.HeadSHA256}, Event: contract.CloneGenerationEvent(*previous)}
	if len(hashes) > 1 {
		result.PreviousRef = contract.GenerationEventRef{Version: 4, Sequence: len(hashes) - 1, SHA256: hashes[len(hashes)-2]}
	}
	return result, nil
}
func (factory *summaryStageFactory) Open(ctx context.Context, purpose string, index int, input, prompt string) (context.Context, error) {
	item, task := factory.item, factory.task
	if factory.index < 0 || item.Recipe == nil || item.Recipe.ModeRecipe.Version != 3 || !recipeHashPattern.MatchString(input) || !recipeHashPattern.MatchString(prompt) {
		return ctx, observerError()
	}
	expected := ""
	switch purpose {
	case "summary-reduce":
		expected = item.Recipe.ModeRecipe.ReducePromptSHA256
	case "summary-verify":
		expected = item.Recipe.ModeRecipe.VerifyPromptSHA256
	default:
		return ctx, observerError()
	}
	if expected != prompt {
		return ctx, observerError()
	}
	key := stageIdentity(purpose, index, input, prompt, item.Source.Hash, item.Recipe.Hash)
	scope := contract.GenerationObservation{ProtocolVersion: 4, Purpose: purpose, StageID: key, TaskID: task.ID, ItemIndex: factory.index, ItemRecipeSHA256: item.Recipe.Hash, SourceSHA256: item.Source.Hash, ChunkIndex: index, ChunkCount: 1, InputSHA256: input, AttemptIndex: -1}
	count := 0
	head := ""
	var previous *contract.GenerationAttemptEvidence
	var priorRef contract.GenerationEventRef
	if item.Stages[key] != nil {
		receipt, err := factory.service.stageReceipt(ctx, *task, factory.index, key)
		if err != nil {
			return ctx, err
		}
		count = receipt.Ref.Sequence
		head = receipt.Ref.SHA256
		event := receipt.Event
		previous = &event
		priorRef = receipt.PreviousRef
	}
	var mutex sync.Mutex
	failed := false
	observer := func(_ context.Context, event contract.GenerationAttemptEvidence) error {
		mutex.Lock()
		defer mutex.Unlock()
		m := event.Observation
		if failed || ctx.Err() != nil || count >= 16 || m.StageID != key || m.TaskID != scope.TaskID || m.ItemIndex != scope.ItemIndex || m.ItemRecipeSHA256 != scope.ItemRecipeSHA256 || m.SourceSHA256 != scope.SourceSHA256 || m.InputSHA256 != scope.InputSHA256 || event.ConfigurationSHA256 != recipeHash(task.Recipe.Configuration) || event.EndpointSHA256 != task.Recipe.Configuration.EndpointSHA256 || !validStageTransition(previous, event, item.Recipe) {
			failed = true
			return observerError()
		}
		directory, err := evidenceDirectory(factory.service.Root, task.ID, factory.index)
		if err != nil {
			failed = true
			return err
		}
		hash := ""
		err = factory.service.withTaskFence(ctx, *task, func() error {
			var err error
			hash, err = publishAttempt(directory, attemptRecord{Version: 4, Sequence: count + 1, PreviousSHA256: head, Event: event})
			return err
		})
		if err != nil {
			failed = true
			return err
		}
		candidate := *task
		candidate.Items = append([]Item(nil), task.Items...)
		stages := map[string]*AttemptEvidenceRef{}
		for k, v := range item.Stages {
			stages[k] = v
		}
		if len(stages) >= 8192 && stages[key] == nil {
			failed = true
			return observerError()
		}
		stages[key] = &AttemptEvidenceRef{Version: 4, HeadSHA256: hash, EventCount: count + 1}
		candidate.Items[factory.index].Stages = stages
		if err := factory.service.save(candidate); err != nil {
			failed = true
			return contract.ObservationFailure(err)
		}
		item.Stages = stages
		priorRef = contract.GenerationEventRef{Version: 4, Sequence: count, SHA256: head}
		count++
		head = hash
		copy := contract.CloneGenerationEvent(event)
		previous = &copy
		return nil
	}
	reader := func(readCtx context.Context) (contract.GenerationReceipt, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if readCtx.Err() != nil || failed || previous == nil {
			return contract.GenerationReceipt{}, contract.ObservationFailure(fmt.Errorf("独立摘要阶段回执不可用"))
		}
		return contract.GenerationReceipt{Ref: contract.GenerationEventRef{Version: 4, Sequence: count, SHA256: head}, PreviousRef: priorRef, Event: contract.CloneGenerationEvent(*previous)}, nil
	}
	ctx = contract.WithGenerationReceiptReader(ctx, reader)
	return contract.WithGenerationObservation(ctx, scope, observer), nil
}

func (factory *summaryStageFactory) LoadArtifact(ctx context.Context, ref string) ([]byte, error) {
	return (treeStorage{factory.service, *factory.task, factory.index}).Get(ctx, ref)
}
func (factory *summaryStageFactory) Check(ctx context.Context, id string, ref contract.GenerationEventRef, purpose, input, output string) error {
	receipt, err := factory.service.stageReceipt(ctx, *factory.task, factory.index, id)
	if err != nil || receipt.Ref != ref || receipt.Event.State != statusFormatResult || receipt.Event.FormatValid == nil || !*receipt.Event.FormatValid || receipt.Event.Observation.Purpose != purpose || receipt.Event.Observation.InputSHA256 != input || receipt.Event.JSONBodySHA256 != output {
		return observerError()
	}
	return nil
}
