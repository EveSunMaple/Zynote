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
		real, err := canonicalEvidencePath(current)
		if err != nil || !strings.EqualFold(filepath.Clean(real), filepath.Clean(current)) {
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
	m := event.Observation
	if event.Version != 1 || m.Purpose != "subtitle-summary" || m.ChunkCount < 1 || m.ChunkCount > totalCues || m.ChunkIndex < 0 || m.ChunkIndex >= m.ChunkCount || m.AttemptIndex < 0 || m.AttemptIndex > 1 || len(m.CueIDs) < 1 || !recipeHashPattern.MatchString(m.InputSHA256) || !recipeHashPattern.MatchString(event.ConfigurationSHA256) || !recipeHashPattern.MatchString(event.EndpointSHA256) || !recipeHashPattern.MatchString(event.RequestBodySHA256) || event.SystemPromptSHA256 != recipe.ModeRecipe.SystemPromptSHA256 {
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
		if event.HTTPStatus != 0 || event.DecodedTextSHA256 != "" || event.FormatValid != nil {
			return false
		}
		if previous == nil {
			return m.ChunkIndex == 0 && m.AttemptIndex == 0
		}
		p := previous.Observation
		if previous.State != "format-result" || previous.FormatValid == nil || p.ChunkCount != m.ChunkCount {
			return false
		}
		if *previous.FormatValid {
			return m.ChunkIndex == p.ChunkIndex+1 && m.AttemptIndex == 0 && m.CueIDs[0] == p.CueIDs[len(p.CueIDs)-1]+1
		}
		return p.AttemptIndex == 0 && m.AttemptIndex == 1 && p.ChunkIndex == m.ChunkIndex && p.InputSHA256 == m.InputSHA256 && reflect.DeepEqual(p.CueIDs, m.CueIDs)
	case "http-response-received":
		return previous != nil && previous.State == "dispatch-intent" && sameAttempt(*previous, event) && event.HTTPStatus >= 100 && event.HTTPStatus <= 599 && event.DecodedTextSHA256 == "" && event.FormatValid == nil
	case "response-decoded":
		return previous != nil && previous.State == "http-response-received" && sameAttempt(*previous, event) && event.HTTPStatus == previous.HTTPStatus && event.HTTPStatus >= 200 && event.HTTPStatus < 300 && recipeHashPattern.MatchString(event.DecodedTextSHA256) && event.FormatValid == nil
	case "format-result":
		return previous != nil && previous.State == "response-decoded" && sameAttempt(*previous, event) && event.HTTPStatus == previous.HTTPStatus && event.DecodedTextSHA256 == previous.DecodedTextSHA256 && event.FormatValid != nil
	}
	return false
}
func (service *Service) observationContext(ctx context.Context, task *Task, item *Item, srt string) (context.Context, error) {
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	if index < 0 || task.Recipe == nil || item.Recipe == nil || item.AttemptEvidence != nil {
		return ctx, observerError()
	}
	segments, err := documents.ParseSrt(srt)
	if err != nil || len(segments) == 0 {
		return ctx, observerError()
	}
	scope := contract.GenerationObservation{Purpose: "subtitle-summary", TaskID: task.ID, ItemIndex: index, ItemRecipeSHA256: item.Recipe.Hash, SourceSHA256: item.Source.Hash, ChunkIndex: -1, AttemptIndex: -1}
	configurationHash := recipeHash(task.Recipe.Configuration)
	var previous *contract.GenerationAttemptEvidence
	var head string
	count := 0
	failed := false
	observer := func(_ context.Context, event contract.GenerationAttemptEvidence) (resultErr error) {
		if failed {
			return observerError()
		}
		defer func() {
			if resultErr != nil {
				failed = true
			}
		}()
		m := event.Observation
		if m.TaskID != scope.TaskID || m.ItemIndex != scope.ItemIndex || m.SourceSHA256 != scope.SourceSHA256 || m.ItemRecipeSHA256 != scope.ItemRecipeSHA256 || event.ConfigurationSHA256 != configurationHash || event.EndpointSHA256 != task.Recipe.Configuration.EndpointSHA256 || !validAttemptTransition(previous, event, len(segments), item.Recipe) || count >= 160000 {
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
		hash, err := publishAttempt(directory, attemptRecord{Version: 1, Sequence: count + 1, PreviousSHA256: head, Event: event})
		if err != nil {
			return err
		}
		candidate := *task
		candidate.Items = append([]Item(nil), task.Items...)
		reference := &AttemptEvidenceRef{Version: 1, HeadSHA256: hash, EventCount: count + 1}
		candidate.Items[index].AttemptEvidence = reference
		if err := service.save(candidate); err != nil {
			return contract.ObservationFailure(err)
		}
		item.AttemptEvidence = reference
		head = hash
		count++
		event.Observation.CueIDs = append([]int(nil), event.Observation.CueIDs...)
		if event.FormatValid != nil {
			value := *event.FormatValid
			event.FormatValid = &value
		}
		previous = &event
		return nil
	}
	return contract.WithGenerationObservation(ctx, scope, observer), nil
}
