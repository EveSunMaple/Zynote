package subtitlederived

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

type AttemptEvidenceRef struct {
	Version    int    `json:"version"`
	HeadSHA256 string `json:"headSHA256"`
	EventCount int    `json:"eventCount"`
}
type attemptRecord struct {
	Version        int                                `json:"version"`
	Sequence       int                                `json:"sequence"`
	PreviousSHA256 string                             `json:"previousSHA256,omitempty"`
	Event          contract.GenerationAttemptEvidence `json:"event"`
}

func observerError() error {
	return contract.ObservationFailure(fmt.Errorf("摘要请求证据无效或保存失败"))
}
func evidenceDirectory(root, taskID string, index int) (string, error) {
	return locateEvidenceDirectory(root, taskID, index, true)
}

func locateEvidenceDirectory(root, taskID string, index int, create bool) (string, error) {
	if !validUUID(taskID) || index < 0 || index >= 4000 {
		return "", observerError()
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", observerError()
	}
	// The configured root is trusted; OS ancestor aliases (macOS /var and
	// Windows short names) are canonicalized. The root itself and every
	// controlled evidence descendant must be ordinary directories.
	rootInfo, err := os.Lstat(absolute)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", observerError()
	}

	resolved, err := canonicalEvidencePath(absolute)
	if err != nil {
		return "", observerError()
	}
	current := resolved
	for _, part := range []string{"evidence", taskID, strconv.Itoa(index)} {
		current = filepath.Join(current, part)
		if create {
			if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
				return "", observerError()
			}
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", observerError()
		}
		resolved, err := canonicalEvidencePath(current)
		if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(current)) {
			return "", observerError()
		}
	}
	return current, nil
}
func publishAttempt(directory string, record attemptRecord) (string, error) {
	body, err := json.Marshal(record)
	if err != nil {
		return "", observerError()
	}
	hash := digest(string(body))
	path := filepath.Join(directory, strconv.Itoa(record.Sequence)+"-"+hash+".json")
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return "", observerError()
	}

	temporary, err := os.CreateTemp(directory, ".attempt-*")
	if err != nil {
		return "", observerError()
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(body)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return "", observerError()
	}
	if err := os.Link(temporary.Name(), path); err != nil {
		return "", observerError()
	}
	check, err := os.ReadFile(path)
	if err != nil || digest(string(check)) != hash {
		return "", observerError()
	}
	return hash, nil
}
func sameAttempt(a, b contract.GenerationAttemptEvidence) bool {
	return reflect.DeepEqual(a.Observation, b.Observation) && a.ConfigurationSHA256 == b.ConfigurationSHA256 && a.EndpointSHA256 == b.EndpointSHA256 && a.SystemPromptSHA256 == b.SystemPromptSHA256 && a.RequestBodySHA256 == b.RequestBodySHA256
}
func validAttemptTransition(previous *contract.GenerationAttemptEvidence, event contract.GenerationAttemptEvidence, totalCues int, recipe *ItemRecipe) bool {
	if event.Version == 3 {
		return validSpanAttemptTransition(previous, event, totalCues, recipe)
	}
	m := event.Observation
	if (event.Version != 1 && event.Version != 2) || (event.Version == 1 && m.ProtocolVersion != 0) || (event.Version == 2 && m.ProtocolVersion != 2) || m.Purpose != "subtitle-summary" || m.ChunkCount < 1 || m.ChunkCount > totalCues || m.ChunkIndex < 0 || m.ChunkIndex >= m.ChunkCount || m.AttemptIndex < 0 || m.AttemptIndex > 1 || len(m.CueIDs) < 1 || !recipeHashPattern.MatchString(m.InputSHA256) || !recipeHashPattern.MatchString(event.ConfigurationSHA256) || !recipeHashPattern.MatchString(event.EndpointSHA256) || !recipeHashPattern.MatchString(event.RequestBodySHA256) || event.SystemPromptSHA256 != recipe.ModeRecipe.SystemPromptSHA256 {
		return false
	}
	for i, id := range m.CueIDs {
		if id < 0 || id >= totalCues || (i > 0 && id != m.CueIDs[i-1]+1) {
			return false
		}
	}
	if (m.ChunkIndex == 0 && m.CueIDs[0] != 0) || (m.ChunkIndex == m.ChunkCount-1 && m.CueIDs[len(m.CueIDs)-1] != totalCues-1) {
		return false
	}
	expectedFeedback := digest("")
	if m.AttemptIndex == 1 {
		expectedFeedback = recipe.ModeRecipe.JSONPolicy.RepairFeedbackSHA256
	}
	if m.FeedbackSHA256 != expectedFeedback {
		return false
	}
	switch event.State {
	case "dispatch-intent":
		if event.HTTPStatus != 0 || event.DecodedTextSHA256 != "" || event.FormatValid != nil || event.ValidatedPoints != nil {
			return false
		}
		if previous == nil {
			return m.ChunkIndex == 0 && m.AttemptIndex == 0
		}
		p := previous.Observation
		if previous.Version != event.Version || previous.FormatValid == nil || p.ChunkCount != m.ChunkCount {
			return false
		}
		if *previous.FormatValid {
			completed := previous.State == statusFormatResult && event.Version == 1 || previous.State == statusValidatedPoints && event.Version == 2
			return completed && m.ChunkIndex == p.ChunkIndex+1 && m.AttemptIndex == 0 && m.CueIDs[0] == p.CueIDs[len(p.CueIDs)-1]+1
		}
		if previous.State != statusFormatResult {
			return false
		}
		return p.AttemptIndex == 0 && m.AttemptIndex == 1 && p.ChunkIndex == m.ChunkIndex && p.InputSHA256 == m.InputSHA256 && reflect.DeepEqual(p.CueIDs, m.CueIDs)
	case "http-response-received":
		return previous != nil && previous.State == "dispatch-intent" && sameAttempt(*previous, event) && event.HTTPStatus >= 100 && event.HTTPStatus <= 599 && event.DecodedTextSHA256 == "" && event.FormatValid == nil && event.ValidatedPoints == nil
	case "response-decoded":
		return previous != nil && previous.State == "http-response-received" && sameAttempt(*previous, event) && event.HTTPStatus == previous.HTTPStatus && event.HTTPStatus >= 200 && event.HTTPStatus < 300 && recipeHashPattern.MatchString(event.DecodedTextSHA256) && event.FormatValid == nil && event.ValidatedPoints == nil
	case statusFormatResult:
		return previous != nil && previous.State == "response-decoded" && sameAttempt(*previous, event) && event.HTTPStatus == previous.HTTPStatus && event.DecodedTextSHA256 == previous.DecodedTextSHA256 && event.FormatValid != nil && event.ValidatedPoints == nil
	case statusValidatedPoints:
		return event.Version == 2 && previous != nil && previous.State == statusFormatResult && previous.FormatValid != nil && *previous.FormatValid && sameAttempt(*previous, event) && event.HTTPStatus == previous.HTTPStatus && event.DecodedTextSHA256 == previous.DecodedTextSHA256 && event.FormatValid != nil && *event.FormatValid && event.ValidatedPoints != nil && event.ValidatedPoints.AcceptedFormatRef.Version == 2 && event.ValidatedPoints.AcceptedFormatRef.Sequence > 0 && recipeHashPattern.MatchString(event.ValidatedPoints.AcceptedFormatRef.SHA256) && recipeHashPattern.MatchString(event.ValidatedPoints.PointsSHA256) && event.ValidatedPoints.PointCount >= 0 && event.ValidatedPoints.PointCount <= recipe.ModeRecipe.MaxPoints
	}
	return false
}
func (service *Service) observationContext(ctx context.Context, task *Task, item *Item, srt string) (context.Context, error) {
	if item.Recipe != nil && item.Recipe.ModeRecipe.Version >= 2 {
		return service.observationContextVersion(ctx, task, item, srt, 3)
	}
	return service.observationContextVersion(ctx, task, item, srt, 2)
}
func (service *Service) observationContextVersion(ctx context.Context, task *Task, item *Item, srt string, traceVersion int) (context.Context, error) {
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	if index < 0 || task.Recipe == nil || item.Recipe == nil || (item.AttemptEvidence != nil && traceVersion != 3) {
		return ctx, observerError()
	}
	segments, err := documents.ParseSrt(srt)
	if err != nil || len(segments) == 0 {
		return ctx, observerError()
	}
	protocolVersion := 0
	maxEvents := 160000
	if traceVersion == 2 || traceVersion == 3 {
		protocolVersion = traceVersion
		maxEvents = 180000
	} else if traceVersion != 1 {
		return ctx, observerError()
	}
	scope := contract.GenerationObservation{ProtocolVersion: protocolVersion, Purpose: "subtitle-summary", TaskID: task.ID, ItemIndex: index, ItemRecipeSHA256: item.Recipe.Hash, SourceSHA256: item.Source.Hash, ChunkIndex: -1, AttemptIndex: -1}
	configurationHash := recipeHash(task.Recipe.Configuration)
	var previous *contract.GenerationAttemptEvidence
	chunkReceipts := map[int]contract.GenerationReceipt{}
	var observationMutex sync.Mutex
	var head string
	count := 0
	failed := false
	if item.AttemptEvidence != nil {
		audit := service.auditAttemptEvidence(ctx, *task, index, false)
		if audit.State != "complete" && audit.State != "incomplete" {
			return ctx, observerError()
		}
		directory, err := locateEvidenceDirectory(service.Root, task.ID, index, false)
		if err != nil {
			return ctx, err
		}
		count = item.AttemptEvidence.EventCount
		head = item.AttemptEvidence.HeadSHA256
		hash := head
		for sequence := count; sequence >= 1; sequence-- {
			record, err := readAttemptRecord(ctx, directory, sequence, hash)
			if err != nil {
				return ctx, err
			}
			event := contract.CloneGenerationEvent(record.Event)
			if sequence == count {
				previous = &event
			}
			if _, ok := chunkReceipts[event.Observation.ChunkIndex]; !ok {
				chunkReceipts[event.Observation.ChunkIndex] = contract.GenerationReceipt{Ref: contract.GenerationEventRef{Version: 3, Sequence: sequence, SHA256: hash}, Event: event}
			}
			hash = record.PreviousSHA256
		}
	}
	observer := func(_ context.Context, event contract.GenerationAttemptEvidence) (resultErr error) {
		observationMutex.Lock()
		defer observationMutex.Unlock()
		if failed {
			return observerError()
		}
		defer func() {
			if resultErr != nil {
				failed = true
			}
		}()
		m := event.Observation
		prior := previous
		expectedRef := contract.GenerationEventRef{Version: traceVersion, Sequence: count, SHA256: head}
		if traceVersion == 3 {
			prior = nil
			if receipt, ok := chunkReceipts[m.ChunkIndex]; ok {
				value := receipt.Event
				prior = &value
				expectedRef = receipt.Ref
			}
		}
		if event.Version != traceVersion || m.TaskID != scope.TaskID || m.ItemIndex != scope.ItemIndex || m.SourceSHA256 != scope.SourceSHA256 || m.ItemRecipeSHA256 != scope.ItemRecipeSHA256 || event.ConfigurationSHA256 != configurationHash || event.EndpointSHA256 != task.Recipe.Configuration.EndpointSHA256 || !validAttemptTransition(prior, event, len(segments), item.Recipe) || count >= maxEvents {
			return observerError()
		}
		if event.State == statusValidatedPoints && event.ValidatedPoints.AcceptedFormatRef != expectedRef {
			return observerError()
		}
		directory, err := evidenceDirectory(service.Root, task.ID, index)
		if err != nil {
			return err
		}
		if count > 0 {
			if item.AttemptEvidence == nil || item.AttemptEvidence.EventCount != count || item.AttemptEvidence.HeadSHA256 != head {
				return observerError()
			}
			path := filepath.Join(directory, strconv.Itoa(count)+"-"+head+".json")
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return observerError()
			}
			raw, err := os.ReadFile(path)
			if err != nil || digest(string(raw)) != head {
				return observerError()
			}
		}
		var hash string
		err = service.withTaskFence(ctx, *task, func() error {
			var err error
			hash, err = publishAttempt(directory, attemptRecord{Version: traceVersion, Sequence: count + 1, PreviousSHA256: head, Event: event})
			return err
		})
		if err != nil {
			return err
		}
		candidate := *task
		candidate.Items = append([]Item(nil), task.Items...)
		reference := &AttemptEvidenceRef{Version: traceVersion, HeadSHA256: hash, EventCount: count + 1}
		candidate.Items[index].AttemptEvidence = reference
		if err := service.save(candidate); err != nil {
			return contract.ObservationFailure(err)
		}
		item.AttemptEvidence = reference
		head = hash
		count++
		event = contract.CloneGenerationEvent(event)
		previous = &event
		if traceVersion == 3 {
			chunkReceipts[m.ChunkIndex] = contract.GenerationReceipt{Ref: contract.GenerationEventRef{Version: 3, Sequence: count, SHA256: head}, Event: event}
		}
		return nil
	}
	reader := func(ctx context.Context) (contract.GenerationReceipt, error) {
		observationMutex.Lock()
		defer observationMutex.Unlock()
		if ctx.Err() != nil || failed || previous == nil || count == 0 {
			return contract.GenerationReceipt{}, observerError()
		}
		if traceVersion == 3 {
			m, observer := contract.GenerationObservationFrom(ctx)
			if observer == nil {
				return contract.GenerationReceipt{}, observerError()
			}
			if m.ChunkIndex >= 0 {
				receipt, ok := chunkReceipts[m.ChunkIndex]
				if !ok {
					return contract.GenerationReceipt{}, observerError()
				}
				receipt.Event = contract.CloneGenerationEvent(receipt.Event)
				return receipt, nil
			}
			if len(chunkReceipts) != previous.Observation.ChunkCount {
				return contract.GenerationReceipt{}, observerError()
			}
			for index := 0; index < previous.Observation.ChunkCount; index++ {
				receipt, ok := chunkReceipts[index]
				if !ok || receipt.Event.State != statusValidatedPoints {
					return contract.GenerationReceipt{}, observerError()
				}
			}
		}
		return contract.GenerationReceipt{Ref: contract.GenerationEventRef{Version: traceVersion, Sequence: count, SHA256: head}, Event: contract.CloneGenerationEvent(*previous)}, nil
	}
	ctx = contract.WithGenerationReceiptReader(ctx, reader)
	return contract.WithGenerationObservation(ctx, scope, observer), nil
}

const statusFormatResult = "format-result"

// v3 keeps the immutable ordered chain but uses chunk identity for transport
// transitions. Actual source core spans are checked independently and replayed
// against the versioned planner by the audit; historical v1/v2 is unchanged.
func validSpanAttemptTransition(previous *contract.GenerationAttemptEvidence, event contract.GenerationAttemptEvidence, totalCues int, recipe *ItemRecipe) bool {
	m := event.Observation
	if m.ProtocolVersion != 3 || recipe.ModeRecipe.Version < 2 || recipe.ModeRecipe.Version > 4 || m.ChunkCount < 1 || m.ChunkCount > 40000 || m.ChunkIndex < 0 || m.ChunkIndex >= m.ChunkCount || len(m.CoreSpans) == 0 || len(m.CoreSpans) != len(m.CueIDs) {
		return false
	}
	for index, span := range m.CoreSpans {
		if span.ParentCueID < 0 || span.ParentCueID >= totalCues || span.ParentCueID != m.CueIDs[index] || span.StartCodepoint < 0 || span.EndCodepoint <= span.StartCodepoint {
			return false
		}
	}
	if event.State == statusValidatedPoints {
		if event.ValidatedPoints == nil || event.ValidatedPoints.Processing == nil {
			return false
		}
		processing := event.ValidatedPoints.Processing
		if (processing.State != "complete" && processing.State != "insufficient") || processing.CoreSpanSHA256 != contract.CoreSpansDigest(m.CoreSpans) {
			return false
		}
	}
	convert := func(input contract.GenerationAttemptEvidence) contract.GenerationAttemptEvidence {
		input = contract.CloneGenerationEvent(input)
		input.Version = 2
		input.Observation.ProtocolVersion = 2
		input.Observation.CueIDs = []int{0}
		input.Observation.ChunkIndex = 0
		input.Observation.ChunkCount = 1
		if input.ValidatedPoints != nil {
			input.ValidatedPoints.AcceptedFormatRef.Version = 2
		}
		return input
	}
	converted := convert(event)
	var prior *contract.GenerationAttemptEvidence
	if previous != nil {
		if previous.Version != 3 || previous.Observation.ChunkIndex != m.ChunkIndex || previous.Observation.ChunkCount != m.ChunkCount {
			return false
		}
		value := convert(*previous)
		prior = &value
	}
	return validAttemptTransition(prior, converted, 1, recipe)
}
