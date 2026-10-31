package subtitlederived

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

type AttemptAudit struct {
	State          string `json:"state"` // unknown, incomplete, invalid, complete
	Reason         string `json:"reason,omitempty"`
	CheckedEvents  int    `json:"checkedEvents"`
	CompleteChunks int    `json:"completeChunks"`
	TotalChunks    int    `json:"totalChunks"`
}

func invalidAudit(reason string) AttemptAudit { return AttemptAudit{State: "invalid", Reason: reason} }
func readAttemptRecord(ctx context.Context, directory string, sequence int, hash string) (attemptRecord, error) {
	if ctx.Err() != nil {
		return attemptRecord{}, ctx.Err()
	}
	if sequence < 1 || sequence > 160000 || !recipeHashPattern.MatchString(hash) {
		return attemptRecord{}, observerError()
	}
	path := filepath.Join(directory, strconv.Itoa(sequence)+"-"+hash+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024 {
		return attemptRecord{}, observerError()
	}
	file, err := os.Open(path)
	if err != nil {
		return attemptRecord{}, observerError()
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 512*1024+1))
	if err != nil || len(body) > 512*1024 || digest(string(body)) != hash {
		return attemptRecord{}, observerError()
	}
	var record attemptRecord
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(&struct{}{}) != io.EOF || record.Version != 1 || record.Sequence != sequence {
		return attemptRecord{}, observerError()
	}
	return record, nil
}

// AuditAttemptEvidence reads only. It never creates directories, saves tasks,
// consults current model settings, adopts orphans or replays provider calls.
func (service *Service) AuditAttemptEvidence(ctx context.Context, task Task, index int) AttemptAudit {
	if ctx.Err() != nil {
		return AttemptAudit{State: "unknown", Reason: "cancelled"}
	}
	if !validUUID(task.ID) || index < 0 || index >= len(task.Items) || len(task.Items) > 4000 {
		return invalidAudit("scope-invalid")
	}
	item := task.Items[index]
	reference := item.AttemptEvidence
	if reference == nil {
		return AttemptAudit{State: "unknown", Reason: "no-observer-record"}
	}
	if reference.Version != 1 || reference.EventCount < 1 || reference.EventCount > 160000 || !recipeHashPattern.MatchString(reference.HeadSHA256) {
		return invalidAudit("reference-invalid")
	}
	if task.EvidenceVersion != 1 || task.Recipe == nil || task.Recipe.Version != 1 || task.Recipe.Origin != "first-new-dispatch" || !validRequestedConfig(task.Recipe.Configuration) || task.Recipe.Hash != taskRecipeHash(task.Recipe.Configuration, task.CourseName) {
		return invalidAudit("task-recipe-invalid")
	}
	if item.Recipe == nil || item.Recipe.Version != 1 || item.Recipe.Origin != "first-new-dispatch" || item.Recipe.TaskRecipeHash != task.Recipe.Hash || item.Recipe.SourceSHA256 != item.Source.Hash || item.Recipe.Hash != itemRecipeHash(*item.Recipe) || item.Recipe.ModeRecipe.Mode != item.Mode || !validMode(item.Mode) {
		return invalidAudit("item-recipe-invalid")
	}
	policy := item.Recipe.ModeRecipe
	if policy.Version != 1 || policy.ChunkPolicy != summaryinput.ChunkPolicy || policy.JSONPolicy.Version != 1 || policy.JSONPolicy.Encoding != "json-no-html-escape" {
		return AttemptAudit{State: "unknown", Reason: "policy-unsupported"}
	}
	if !validModeRecipe(policy, item.Mode) {
		return invalidAudit("item-policy-invalid")
	}
	if !inside(service.Library, item.Source.Path) || !recipeHashPattern.MatchString(item.Source.Hash) {
		return invalidAudit("source-path-invalid")
	}
	library, err := canonicalEvidencePath(service.Library)
	if err != nil {
		return AttemptAudit{State: "unknown", Reason: "source-unverifiable"}
	}
	source, err := canonicalEvidencePath(item.Source.Path)
	if err != nil {
		return AttemptAudit{State: "unknown", Reason: "source-unverifiable"}
	}
	if !inside(library, source) {
		return invalidAudit("source-path-invalid")
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32*1024*1024 {
		return AttemptAudit{State: "unknown", Reason: "source-unverifiable"}
	}
	file, err := os.Open(source)
	if err != nil {
		return AttemptAudit{State: "unknown", Reason: "source-unverifiable"}
	}
	body, readErr := io.ReadAll(io.LimitReader(file, 32*1024*1024+1))
	closeErr := file.Close()
	if ctx.Err() != nil {
		return AttemptAudit{State: "unknown", Reason: "cancelled"}
	}
	if readErr != nil || closeErr != nil || len(body) > 32*1024*1024 {
		return AttemptAudit{State: "unknown", Reason: "source-unverifiable"}
	}
	if digest(string(body)) != item.Source.Hash {
		return invalidAudit("source-changed")
	}
	segments, err := documents.ParseSrt(string(body))
	if err != nil || len(segments) < 1 || len(segments) > 20000 {
		return invalidAudit("source-invalid")
	}
	parts := summaryinput.Chunks(segments, policy.ChunkLimit)
	inputHashes := make([]string, len(parts))
	starts := make([]int, len(parts))
	start := 0
	for chunkIndex, part := range parts {
		if ctx.Err() != nil {
			return AttemptAudit{State: "unknown", Reason: "cancelled"}
		}
		starts[chunkIndex] = start
		hash, err := summaryinput.Digest(summaryinput.New(item.Mode, part, start))
		if err != nil {
			return invalidAudit("input-contract-invalid")
		}
		inputHashes[chunkIndex] = hash
		start += len(part)
	}
	if reference.EventCount > len(segments)*8 {
		return invalidAudit("event-count-invalid")
	}
	directory, err := locateEvidenceDirectory(service.Root, task.ID, index, false)
	if err != nil {
		return invalidAudit("evidence-path-invalid")
	}
	// First walk hashes backwards; only bounded hashes are retained in memory.
	hashes := make([]string, reference.EventCount)
	hash := reference.HeadSHA256
	for sequence := reference.EventCount; sequence >= 1; sequence-- {
		if ctx.Err() != nil {
			return AttemptAudit{State: "unknown", Reason: "cancelled"}
		}
		record, err := readAttemptRecord(ctx, directory, sequence, hash)
		if err != nil {
			if ctx.Err() != nil {
				return AttemptAudit{State: "unknown", Reason: "cancelled"}
			}
			return invalidAudit("chain-invalid")
		}
		hashes[sequence-1] = hash
		if sequence == 1 {
			if record.PreviousSHA256 != "" {
				return invalidAudit("chain-invalid")
			}
		} else if !recipeHashPattern.MatchString(record.PreviousSHA256) {
			return invalidAudit("chain-invalid")
		}
		hash = record.PreviousSHA256
	}
	// Then replay every event forward, re-reading and hashing each immutable file.
	configurationHash := recipeHash(task.Recipe.Configuration)
	var previous *contract.GenerationAttemptEvidence
	result := AttemptAudit{State: "incomplete"}
	for indexInChain, hash := range hashes {
		if ctx.Err() != nil {
			return AttemptAudit{State: "unknown", Reason: "cancelled"}
		}
		record, err := readAttemptRecord(ctx, directory, indexInChain+1, hash)
		if err != nil {
			if ctx.Err() != nil {
				return AttemptAudit{State: "unknown", Reason: "cancelled"}
			}
			return invalidAudit("chain-invalid")
		}
		if indexInChain > 0 && record.PreviousSHA256 != hashes[indexInChain-1] {
			return invalidAudit("chain-invalid")
		}
		event := record.Event
		m := event.Observation
		if m.TaskID != task.ID || m.ItemIndex != index || m.SourceSHA256 != item.Source.Hash || m.ItemRecipeSHA256 != item.Recipe.Hash || event.ConfigurationSHA256 != configurationHash || event.EndpointSHA256 != task.Recipe.Configuration.EndpointSHA256 || !validAttemptTransition(previous, event, len(segments), item.Recipe) {
			return invalidAudit("event-invalid")
		}
		if m.AttemptIndex >= policy.JSONPolicy.MaxAttempts || m.ChunkCount != len(parts) || m.ChunkIndex >= len(parts) || m.InputSHA256 != inputHashes[m.ChunkIndex] || len(m.CueIDs) != len(parts[m.ChunkIndex]) || m.CueIDs[0] != starts[m.ChunkIndex] {
			return invalidAudit("input-contract-invalid")
		}
		result.CheckedEvents++
		result.TotalChunks = m.ChunkCount
		if event.State == "format-result" && event.FormatValid != nil && *event.FormatValid {
			result.CompleteChunks++
		}
		previous = &event
	}
	if previous != nil && previous.State == "format-result" && previous.FormatValid != nil && *previous.FormatValid && previous.Observation.ChunkIndex == previous.Observation.ChunkCount-1 && result.CompleteChunks == result.TotalChunks {
		result.State = "complete"
	}
	return result
}
