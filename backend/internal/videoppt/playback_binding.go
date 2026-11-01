package videoppt

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

func samePlaybackLocation(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// VerifiedRetainedSubtitle can only be produced by the retention validator.
// Its zero value and descriptors from another library/course are unusable.
type VerifiedRetainedSubtitle struct {
	binding                     contract.PlaybackSourceBinding
	srt, title, course, library string
	declaration                 PlaybackDeclaration
}

func PlaybackBindingSHA256(binding contract.PlaybackSourceBinding) string {
	body, _ := json.Marshal(binding)
	return retentionHash(body)
}

func ValidPlaybackBinding(binding contract.PlaybackSourceBinding) bool {
	path := filepath.FromSlash(binding.SavedSubtitle)
	return binding.Version == 1 && operationIDPattern.MatchString(binding.VideoTaskID) &&
		playbackDigestPattern.MatchString(binding.SourceSHA256) && playbackDigestPattern.MatchString(binding.SubtitleSHA256) &&
		playbackDigestPattern.MatchString(binding.RetentionReceiptSHA256) && filepath.IsLocal(path) &&
		filepath.ToSlash(filepath.Clean(path)) == binding.SavedSubtitle && strings.HasPrefix(binding.SavedSubtitle, "Transcripts/")
}

// ForCreation revalidates the encapsulated descriptor at its destination.
func (verified VerifiedRetainedSubtitle) ForCreation(ctx context.Context, libraryRoot, course string) (contract.PlaybackSourceBinding, string, string, error) {
	if !ValidPlaybackBinding(verified.binding) || !samePlaybackLocation(verified.library, libraryRoot) || verified.course != course {
		return contract.PlaybackSourceBinding{}, "", "", retentionConflict()
	}
	fresh, err := New(filepath.Join(libraryRoot, "VideoTasks")).ValidateRetainedPlaybackBinding(ctx, verified.binding, course)
	if err != nil {
		return contract.PlaybackSourceBinding{}, "", "", err
	}
	return fresh.binding, fresh.srt, fresh.title, nil
}

func (verified VerifiedRetainedSubtitle) Declaration() PlaybackDeclaration {
	return verified.declaration
}

// RetainedSubtitle resolves a caller's locator to a trusted descriptor, without
// accepting a title, saved path, subtitle bytes or claimed source identity.
func (service *Service) RetainedSubtitle(ctx context.Context, id, receiptSHA, course string) (VerifiedRetainedSubtitle, error) {
	if !operationIDPattern.MatchString(id) || !playbackDigestPattern.MatchString(receiptSHA) {
		return VerifiedRetainedSubtitle{}, retentionConflict()
	}
	if err := service.CheckPlaybackDispatch(ctx, id); err != nil {
		return VerifiedRetainedSubtitle{}, err
	}
	var receipt retentionReceipt
	ref, err := retentionRecord(ctx, filepath.Join(service.Root, id, "retention-receipt.json"), false, &receipt)
	if err != nil {
		return VerifiedRetainedSubtitle{}, err
	}
	if ref != receiptSHA {
		return VerifiedRetainedSubtitle{}, retentionConflict()
	}
	binding := contract.PlaybackSourceBinding{Version: 1, VideoTaskID: id, SourceSHA256: receipt.SourceSHA256, SubtitleSHA256: receipt.SubtitleSHA256, RetentionReceiptSHA256: receiptSHA, SavedSubtitle: receipt.SavedSubtitle}
	return service.ValidateRetainedPlaybackBinding(ctx, binding, course)
}

// ValidateRetainedPlaybackBinding is read-only and never hashes video bytes.
func (service *Service) ValidateRetainedPlaybackBinding(ctx context.Context, binding contract.PlaybackSourceBinding, course string) (VerifiedRetainedSubtitle, error) {
	empty := VerifiedRetainedSubtitle{}
	if !ValidPlaybackBinding(binding) {
		return empty, retentionConflict()
	}
	id := binding.VideoTaskID
	if err := service.CheckPlaybackDispatch(ctx, id); err != nil {
		return empty, err
	}
	index, _, err := service.retentionIndex(ctx, false)
	if err != nil {
		return empty, err
	}
	entry, exists := index.Entries[id]
	if !exists {
		return empty, retentionConflict()
	}
	if entry.State != "retained" || entry.ReceiptSHA256 != binding.RetentionReceiptSHA256 || entry.SourceSHA256 != binding.SourceSHA256 {
		return empty, retentionConflict()
	}
	directory := filepath.Join(service.Root, id)
	var intent retentionIntent
	intentRef, err := retentionRecord(ctx, filepath.Join(directory, "retention-intent.json"), false, &intent)
	if err != nil {
		return empty, err
	}
	if intentRef != entry.IntentSHA256 || intent.Version != 1 || intent.ID != id || intent.SourceSHA256 != entry.SourceSHA256 || intent.Size != entry.Size {
		return empty, retentionConflict()
	}
	var operation VideoOperation
	_, err = retentionRecord(ctx, filepath.Join(directory, "operation.json"), false, &operation)
	if err != nil {
		return empty, err
	}
	operationBody, _ := json.Marshal(operation)
	if operation.Version != 1 || operation.Stage != "subtitle" || !operation.RetainPlayback || operation.CourseName != course || operation.SourceHash != binding.SourceSHA256 || retentionHash(operationBody) != intent.OperationSHA256 {
		return empty, retentionConflict()
	}
	var receipt retentionReceipt
	ref, err := retentionRecord(ctx, filepath.Join(directory, "retention-receipt.json"), false, &receipt)
	if err != nil {
		return empty, err
	}
	if ref != entry.ReceiptSHA256 || receipt.Version != 1 || receipt.ID != id || receipt.SourceSHA256 != binding.SourceSHA256 || receipt.SubtitleSHA256 != binding.SubtitleSHA256 || receipt.SavedSubtitle != binding.SavedSubtitle || receipt.CandidateSHA256 != entry.CandidateSHA256 {
		return empty, retentionConflict()
	}
	var candidate retentionCandidate
	candidateRef, err := retentionRecord(ctx, filepath.Join(directory, "retention-candidate.json"), false, &candidate)
	if err != nil {
		return empty, err
	}
	if candidateRef != receipt.CandidateSHA256 || candidate.Version != 1 || candidate.ID != id || candidate.SourceSHA256 != binding.SourceSHA256 || candidate.SubtitleSHA256 != binding.SubtitleSHA256 || candidate.IntentSHA256 != entry.IntentSHA256 || candidate.OperationSHA256 != intent.OperationSHA256 {
		return empty, retentionConflict()
	}
	root := filepath.Dir(service.Root)
	path := filepath.Join(root, filepath.FromSlash(binding.SavedSubtitle))
	courseDirectory := filepath.Join(root, "Transcripts")
	if course != "" {
		courseDirectory = filepath.Join(courseDirectory, library.FolderName(course))
	}
	if !samePlaybackLocation(filepath.Dir(path), courseDirectory) {
		return empty, retentionConflict()
	}
	// Every parent is checked, including junction aliases on Windows.
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if _, err := playbackPath(parent, true); err != nil {
			return empty, err
		}
		if samePlaybackLocation(parent, root) {
			break
		}
		if filepath.Dir(parent) == parent {
			return empty, retentionConflict()
		}
	}
	body, err := retentionFile(ctx, path, 4000000)
	if err != nil {
		return empty, err
	}
	if retentionHash(body) != binding.SubtitleSHA256 {
		return empty, retentionConflict()
	}
	segments, err := documents.ParseSrt(string(body))
	if err != nil {
		return empty, err
	}
	if len(segments) < 1 || len(segments) > 20000 {
		return empty, retentionConflict()
	}
	declaration, err := service.PlaybackSource(ctx, id, binding.SourceSHA256)
	if err != nil {
		return empty, err
	}
	fence, _ := json.Marshal([]string{declaration.BindingRevision, PlaybackBindingSHA256(binding), ref, candidateRef, entry.IntentSHA256, retentionHash(body)})
	declaration.BindingRevision = retentionHash(fence)
	title := operation.OutputName
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if title == "" || len([]rune(title)) > 200 {
		return empty, retentionConflict()
	}
	return VerifiedRetainedSubtitle{binding: binding, srt: string(body), title: title, course: course, library: root, declaration: declaration}, nil
}
