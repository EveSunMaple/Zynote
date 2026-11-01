package videoppt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

const retentionMaximumItems = 16
const retentionMaximumBytes = int64(32 * 1024 * 1024 * 1024)

type retentionMutex struct{ gate chan struct{} }

var retentionMu = retentionMutex{make(chan struct{}, 1)}

func (mutex *retentionMutex) Lock(ctx context.Context) error {
	select {
	case mutex.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-mutex.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (mutex *retentionMutex) Unlock() { <-mutex.gate }

// Unexported process-loss seam; nil in production.
var retentionBoundary func(string)

func retentionReached(name string) {
	if retentionBoundary != nil {
		retentionBoundary(name)
	}
}

type PlaybackBinding struct {
	Version                int    `json:"version"`
	VideoTaskID            string `json:"videoTaskID"`
	SourceSHA256           string `json:"sourceSHA256"`
	SubtitleSHA256         string `json:"subtitleSHA256"`
	RetentionReceiptSHA256 string `json:"retentionReceiptSHA256"`
}
type RetentionEntry struct {
	ID              string `json:"id"`
	Size            int64  `json:"size"`
	SourceSHA256    string `json:"sourceSHA256"`
	IntentSHA256    string `json:"intentSHA256"`
	ReceiptSHA256   string `json:"receiptSHA256,omitempty"`
	CandidateSHA256 string `json:"candidateSHA256,omitempty"`
	State           string `json:"state"`
}
type retentionIndex struct {
	Version int                       `json:"version"`
	Entries map[string]RetentionEntry `json:"entries"`
}
type retentionIntent struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	SourceSHA256    string `json:"sourceSHA256"`
	Size            int64  `json:"size"`
	OperationSHA256 string `json:"operationSHA256"`
}
type retentionCandidate struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	SourceSHA256    string `json:"sourceSHA256"`
	SubtitleSHA256  string `json:"subtitleSHA256"`
	IntentSHA256    string `json:"intentSHA256"`
	OperationSHA256 string `json:"operationSHA256"`
}
type retentionReceipt struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	SourceSHA256    string `json:"sourceSHA256"`
	SubtitleSHA256  string `json:"subtitleSHA256"`
	CandidateSHA256 string `json:"candidateSHA256"`
	SavedSubtitle   string `json:"savedSubtitle"`
}

func retentionHash(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
func retentionConflict() error {
	return contract.NewError(contract.CodeConflict, "视频留存身份不一致，原文件已保留")
}
func retentionRecord(ctx context.Context, path string, optional bool, target any) (string, error) {
	body, err := playbackMetadata(ctx, path, optional)
	if err != nil {
		return "", err
	}
	if body == nil && optional {
		return "", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return "", retentionConflict()
	}
	return retentionHash(body), nil
}
func immutableRetention(path string, value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(body) > 65536 {
		return "", retentionConflict()
	}
	if existing, err := playbackMetadata(context.Background(), path, true); err != nil {
		return "", err
	} else if existing != nil {
		if !bytes.Equal(existing, body) {
			return "", retentionConflict()
		}
		return retentionHash(body), nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		existing, readErr := playbackMetadata(context.Background(), path, false)
		if readErr != nil || !bytes.Equal(existing, body) {
			return "", retentionConflict()
		}
		return retentionHash(body), nil
	}
	if err != nil {
		return "", err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	// A partial record is retained, never overwritten or treated as committed.
	// The pre-existing reservation exposes it for explicit fenced release.
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return retentionHash(body), nil
}
func (service *Service) retentionIndex(ctx context.Context, create bool) (retentionIndex, string, error) {
	empty := retentionIndex{Version: 1, Entries: map[string]RetentionEntry{}}
	if err := service.playbackRoot(); err != nil {
		return empty, "", err
	}
	if _, err := playbackPath(service.Root, true); err != nil {
		return empty, "", err
	}
	directory := filepath.Join(service.Root, ".retained")
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		if !create {
			return empty, "", nil
		}
		if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
			return empty, "", err
		}
	}
	if _, err := playbackPath(directory, true); err != nil {
		return empty, "", err
	}
	path := filepath.Join(directory, "index.json")
	ref, err := retentionRecord(ctx, path, true, &empty)
	if err != nil {
		return empty, "", err
	}
	if ref == "" {
		return empty, path, nil
	}
	if empty.Version != 1 || empty.Entries == nil || len(empty.Entries) > retentionMaximumItems {
		return empty, "", retentionConflict()
	}
	total := int64(0)
	for id, entry := range empty.Entries {
		total += entry.Size
		if entry.State != "reserved" && entry.State != "candidate" && entry.State != "retained" && entry.State != "releasing" {
			return empty, "", retentionConflict()
		}
		if !operationIDPattern.MatchString(id) || entry.ID != id || entry.Size <= 0 || entry.Size > 8*1024*1024*1024 || !playbackDigestPattern.MatchString(entry.SourceSHA256) || !playbackDigestPattern.MatchString(entry.IntentSHA256) || (entry.CandidateSHA256 != "" && !playbackDigestPattern.MatchString(entry.CandidateSHA256)) || (entry.ReceiptSHA256 != "" && !playbackDigestPattern.MatchString(entry.ReceiptSHA256)) {
			return empty, "", retentionConflict()
		}
	}
	if total > retentionMaximumBytes {
		return empty, "", retentionConflict()
	}
	return empty, path, nil
}

// ReservePlaybackRetention is called before queue dispatch/ASR. A durable index
// reservation precedes intent creation, so failed writes cannot evade disk quotas.
func (service *Service) ReservePlaybackRetention(ctx context.Context, id string, size int64) (RetentionEntry, error) {
	if err := retentionMu.Lock(ctx); err != nil {
		return RetentionEntry{}, err
	}
	defer retentionMu.Unlock()
	var empty RetentionEntry
	if err := service.CheckPlaybackDispatch(ctx, id); err != nil {
		return empty, err
	}
	operation, err := service.Operation(id)
	if err != nil {
		return empty, err
	}
	if !playbackDigestPattern.MatchString(operation.SourceHash) || !operation.RetainPlayback || operation.Stage != "subtitle" || operation.Path == "" || operation.URL != "" || size <= 0 || size > 8*1024*1024*1024 {
		return empty, contract.InputInvalid("视频留存仅支持新建本地字幕任务，且媒体不得超过8GiB")
	}
	if _, err := operationSource(operation); err != nil {
		return empty, retentionConflict()
	}
	opBody, _ := json.Marshal(operation)
	intent := retentionIntent{1, id, operation.SourceHash, size, retentionHash(opBody)}
	intentBody, _ := json.Marshal(intent)
	entry := RetentionEntry{ID: id, Size: size, SourceSHA256: operation.SourceHash, IntentSHA256: retentionHash(intentBody), State: "reserved"}
	index, path, err := service.retentionIndex(ctx, true)
	if err != nil {
		return empty, err
	}
	if existing, ok := index.Entries[id]; ok {
		if existing.IntentSHA256 != entry.IntentSHA256 {
			return empty, retentionConflict()
		}
		entry = existing
	} else {
		total := size
		for _, existing := range index.Entries {
			total += existing.Size
		}
		if len(index.Entries) >= retentionMaximumItems || total > retentionMaximumBytes {
			return empty, contract.NewError(contract.CodeBusy, "视频留存额度已满，请先释放保留的视频")
		}
		index.Entries[id] = entry
		if err := saveTaskJSON(path, index); err != nil {
			return empty, err
		}
	}
	retentionReached("reservation-index")
	directory, err := service.location(id)
	if err != nil {
		return empty, err
	}
	if _, err := immutableRetention(filepath.Join(directory, "retention-intent.json"), intent); err != nil {
		return empty, err
	}
	retentionReached("intent-written")
	return entry, nil
}
func (service *Service) RetainedPlayback(ctx context.Context, limit int) ([]RetentionEntry, error) {
	if limit < 1 || limit > retentionMaximumItems {
		return nil, contract.InputInvalid("留存列表数量无效")
	}
	if err := retentionMu.Lock(ctx); err != nil {
		return nil, err
	}
	defer retentionMu.Unlock()
	if err := service.playbackRoot(); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(service.Root); os.IsNotExist(err) {
		return []RetentionEntry{}, nil
	}
	index, _, err := service.retentionIndex(ctx, false)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(index.Entries))
	for id := range index.Entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := []RetentionEntry{}
	for _, id := range ids {
		if len(result) == limit {
			break
		}
		result = append(result, index.Entries[id])
	}
	return result, nil
}
func (service *Service) retentionBound(ctx context.Context, id string) (retentionIndex, string, RetentionEntry, retentionIntent, error) {
	index, path, err := service.retentionIndex(ctx, false)
	if err != nil {
		return index, path, RetentionEntry{}, retentionIntent{}, err
	}
	entry, ok := index.Entries[id]
	if !ok {
		return index, path, entry, retentionIntent{}, retentionConflict()
	}
	directory, err := service.location(id)
	if err != nil {
		return index, path, entry, retentionIntent{}, err
	}
	var intent retentionIntent
	ref, err := retentionRecord(ctx, filepath.Join(directory, "retention-intent.json"), false, &intent)
	if err != nil || ref != entry.IntentSHA256 || intent.Version != 1 || intent.ID != id || intent.SourceSHA256 != entry.SourceSHA256 || intent.Size != entry.Size {
		return index, path, entry, intent, retentionConflict()
	}
	operation, err := service.Operation(id)
	body, _ := json.Marshal(operation)
	if err != nil || !operation.RetainPlayback || retentionHash(body) != intent.OperationSHA256 {
		return index, path, entry, intent, retentionConflict()
	}
	return index, path, entry, intent, nil
}
func (service *Service) PreparePlaybackRetention(ctx context.Context, id, srt string) error {
	if err := retentionMu.Lock(ctx); err != nil {
		return err
	}
	defer retentionMu.Unlock()
	if err := service.CheckPlaybackDispatch(ctx, id); err != nil {
		return err
	}
	index, path, entry, intent, err := service.retentionBound(ctx, id)
	if err != nil {
		return err
	}
	if len(srt) > 32*1024*1024 {
		return retentionConflict()
	}
	if _, err := documents.ParseSrt(srt); err != nil {
		return retentionConflict()
	}
	if _, err := service.PlaybackSource(ctx, id, entry.SourceSHA256); err != nil {
		return err
	}
	candidate := retentionCandidate{1, id, entry.SourceSHA256, retentionHash([]byte(srt)), entry.IntentSHA256, intent.OperationSHA256}
	candidateRef, err := immutableRetention(filepath.Join(service.Root, id, "retention-candidate.json"), candidate)
	if err != nil {
		return err
	}
	retentionReached("candidate-written")
	entry.CandidateSHA256 = candidateRef
	entry.State = "candidate"
	index.Entries[id] = entry
	if err := saveTaskJSON(path, index); err != nil {
		return err
	}
	retentionReached("candidate-index")
	return nil
}

var retentionInputFile = regexp.MustCompile(`^input-subtitle-[a-f0-9]{64}\.json$`)
var retentionExportFile = regexp.MustCompile(`^subtitle-export-[a-f0-9]{64}\.json$`)

func retentionTemporary(name string) bool {
	return name == "subtitle-prepared.json" || retentionInputFile.MatchString(name) || retentionExportFile.MatchString(name)
}

// removeStagingFiles 清理进程被 kill 时遗留的 .video-copy-* 临时文件；这些文件不属于任务清单。
func removeStagingFiles(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".video-copy-") {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func retentionKeep(name string) bool {
	switch name {
	case "video.mp4", "operation.json", "video-ready.json", "media-source.json", "task.json", "retention-intent.json", "retention-candidate.json", "retention-receipt.json":
		return true
	}
	return false
}
func (service *Service) CompletePlaybackRetention(ctx context.Context, id, savedPath string) (*PlaybackBinding, error) {
	if err := retentionMu.Lock(ctx); err != nil {
		return nil, err
	}
	defer retentionMu.Unlock()
	if err := service.CheckPlaybackDispatch(ctx, id); err != nil {
		return nil, err
	}
	index, indexPath, entry, intent, err := service.retentionBound(ctx, id)
	if err != nil {
		return nil, err
	}
	directory, err := service.location(id)
	if err != nil {
		return nil, err
	}
	var candidate retentionCandidate
	candidateRef, err := retentionRecord(ctx, filepath.Join(directory, "retention-candidate.json"), false, &candidate)
	if err != nil || candidate.Version != 1 || candidate.ID != id || candidate.IntentSHA256 != entry.IntentSHA256 || candidate.SourceSHA256 != entry.SourceSHA256 || candidateRef != entry.CandidateSHA256 || candidate.OperationSHA256 != intent.OperationSHA256 {
		return nil, retentionConflict()
	}
	// Only an already-durable candidate can authorize a binding. Saved-only recovery
	// never calls PreparePlaybackRetention to invent a historical candidate.
	libraryRoot := filepath.Dir(service.Root)
	relative, err := filepath.Rel(libraryRoot, savedPath)
	if err != nil || relative == "" || strings.HasPrefix(relative, "..") || !strings.HasPrefix(relative, "Transcripts"+string(filepath.Separator)) {
		return nil, retentionConflict()
	}
	info, err := playbackPath(savedPath, false)
	if err != nil || info.Size() > 32*1024*1024 {
		return nil, retentionConflict()
	}
	body, err := retentionFile(ctx, savedPath, 32*1024*1024)
	if err != nil || retentionHash(body) != candidate.SubtitleSHA256 {
		return nil, retentionConflict()
	}
	if _, err := documents.ParseSrt(string(body)); err != nil {
		return nil, retentionConflict()
	}
	if _, err := service.PlaybackSource(ctx, id, entry.SourceSHA256); err != nil {
		return nil, err
	}
	receipt := retentionReceipt{1, id, entry.SourceSHA256, candidate.SubtitleSHA256, candidateRef, filepath.ToSlash(relative)}
	receiptRef, err := immutableRetention(filepath.Join(directory, "retention-receipt.json"), receipt)
	if err != nil {
		return nil, err
	}
	retentionReached("receipt-written")
	if err := removeStagingFiles(directory); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, file := range entries {
		if !retentionKeep(file.Name()) && !retentionTemporary(file.Name()) {
			return nil, contract.Unavailable("字幕已保存，任务含未知文件，未自动清理或发布播放绑定")
		}
		if _, err := playbackPath(filepath.Join(directory, file.Name()), false); err != nil {
			return nil, err
		}
	}
	for _, file := range entries {
		if retentionTemporary(file.Name()) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := os.Remove(filepath.Join(directory, file.Name())); err != nil {
				return nil, err
			}
			retentionReached("retention-cleanup-one")
		}
	}
	entry.State = "retained"
	entry.ReceiptSHA256 = receiptRef
	index.Entries[id] = entry
	if err := saveTaskJSON(indexPath, index); err != nil {
		return nil, err
	}
	retentionReached("retained-index")
	return &PlaybackBinding{1, id, entry.SourceSHA256, candidate.SubtitleSHA256, receiptRef}, nil
}
func (service *Service) HasRetentionCandidate(id string) bool {
	_, err := os.Lstat(filepath.Join(service.Root, id, "retention-candidate.json"))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func retentionFile(ctx context.Context, path string, maximum int64) ([]byte, error) {
	before, err := playbackPath(path, false)
	if err != nil || before.Size() > maximum {
		return nil, retentionConflict()
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, retentionConflict()
	}
	var buffer bytes.Buffer
	chunk := make([]byte, 64*1024)
	reader := io.LimitReader(file, maximum+1)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(chunk)
		buffer.Write(chunk[:n])
		if int64(buffer.Len()) > maximum {
			return nil, retentionConflict()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	after, err := playbackPath(path, false)
	if err != nil || int64(buffer.Len()) != opened.Size() || before.Size() != opened.Size() || !before.ModTime().Equal(opened.ModTime()) || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, retentionConflict()
	}
	return buffer.Bytes(), nil
}

// Retry can encounter a quota/write failure after operation creation. The actual
// execution callback must re-establish the reservation before copy/ASR work.
func (service *Service) EnsurePlaybackRetention(ctx context.Context, id string) error {
	operation, err := service.Operation(id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !operation.RetainPlayback {
		return nil
	}
	if err := retentionMu.Lock(ctx); err != nil {
		return err
	}
	index, _, indexErr := service.retentionIndex(ctx, false)
	size := int64(0)
	if indexErr == nil {
		if entry, ok := index.Entries[id]; ok {
			size = entry.Size
		}
	}
	retentionMu.Unlock()
	if indexErr != nil {
		return indexErr
	}
	if size == 0 {
		info, err := playbackPath(operation.Path, false)
		if err != nil {
			return err
		}
		size = info.Size()
	}
	_, err = service.ReservePlaybackRetention(ctx, id, size)
	return err
}

func (service *Service) retainedCompleted(ctx context.Context, id string, index retentionIndex) bool {
	entry, ok := index.Entries[id]
	if !ok || entry.State != "retained" || entry.ReceiptSHA256 == "" {
		return false
	}
	var receipt retentionReceipt
	ref, err := retentionRecord(ctx, filepath.Join(service.Root, id, "retention-receipt.json"), false, &receipt)
	if err != nil || ref != entry.ReceiptSHA256 || receipt.ID != id || receipt.SourceSHA256 != entry.SourceSHA256 {
		return false
	}
	body, err := retentionFile(ctx, filepath.Join(service.Root, ".completed", id+".json"), 64*1024*1024)
	if err != nil {
		return false
	}
	var completed operationCompletion
	if json.Unmarshal(body, &completed) != nil || !completed.Operation.RetainPlayback {
		return false
	}
	var result struct {
		Binding *PlaybackBinding `json:"playbackBinding"`
	}
	if json.Unmarshal(completed.Result, &result) != nil || result.Binding == nil {
		return false
	}
	binding := result.Binding
	return binding.Version == 1 && binding.VideoTaskID == id && binding.SourceSHA256 == entry.SourceSHA256 && binding.RetentionReceiptSHA256 == ref && binding.SubtitleSHA256 == receipt.SubtitleSHA256
}
