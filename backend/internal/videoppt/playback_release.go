package videoppt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type PlaybackReleaseRequest struct {
	ID                      string `json:"id"`
	ExpectedRefKind         string `json:"expectedRefKind"`
	ExpectedRefSHA256       string `json:"expectedRefSHA256"`
	ReleaseID               string `json:"releaseID"`
	ExpectedTombstoneSHA256 string `json:"expectedTombstoneSHA256,omitempty"`
}
type playbackReleaseFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type PlaybackReleaseResult struct {
	Plan            PlaybackReleasePlan `json:"plan"`
	TombstoneSHA256 string              `json:"tombstoneSHA256"`
}

type PlaybackReleasePlan struct {
	Version   int                   `json:"version"`
	ID        string                `json:"id"`
	RefKind   string                `json:"refKind"`
	RefSHA256 string                `json:"refSHA256"`
	ReleaseID string                `json:"releaseID"`
	State     string                `json:"state"`
	Files     []playbackReleaseFile `json:"files"`
}

func releaseRequestValid(input PlaybackReleaseRequest) bool {
	return operationIDPattern.MatchString(input.ID) && operationIDPattern.MatchString(input.ReleaseID) && (input.ExpectedRefKind == "intent" || input.ExpectedRefKind == "receipt") && playbackDigestPattern.MatchString(input.ExpectedRefSHA256)
}
func releaseFileMaximum(name string) int64 {
	if name == videoFileName {
		return 8 * 1024 * 1024 * 1024
	}
	if name == "subtitle-prepared.json" {
		return 6*32*1024*1024 + 65536
	}
	return 65536
}
func releaseEntryMatches(entry RetentionEntry, kind, ref string) bool {
	if entry.ReceiptSHA256 != "" {
		return kind == "receipt" && ref == entry.ReceiptSHA256
	}
	return kind == "intent" && ref == entry.IntentSHA256
}
func releaseMediaIdentityAllowed(file playbackReleaseFile, entry RetentionEntry) bool {
	if file.Name != videoFileName {
		return true
	}
	if file.Size > entry.Size {
		return false
	}
	if file.Size == entry.Size {
		return file.SHA256 == entry.SourceSHA256
	}
	return entry.ReceiptSHA256 == "" // Only an uncommitted failed partial copy.
}
func releasePlanMediaAllowed(plan PlaybackReleasePlan, entry RetentionEntry) bool {
	for _, file := range plan.Files {
		if !releaseMediaIdentityAllowed(file, entry) {
			return false
		}
	}
	return true
}
func releaseFileAllowed(name string) bool { return retentionKeep(name) || retentionTemporary(name) }
func releaseDigest(ctx context.Context, path string, maximum int64) (playbackReleaseFile, error) {
	before, err := playbackPath(path, false)
	if err != nil || before.Size() < 0 || before.Size() > maximum {
		return playbackReleaseFile{}, retentionConflict()
	}
	file, err := os.Open(path)
	if err != nil {
		return playbackReleaseFile{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return playbackReleaseFile{}, retentionConflict()
	}
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return playbackReleaseFile{}, err
		}
		n, readErr := file.Read(buffer)
		size += int64(n)
		if size > maximum {
			return playbackReleaseFile{}, retentionConflict()
		}
		hash.Write(buffer[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return playbackReleaseFile{}, readErr
		}
	}
	after, err := playbackPath(path, false)
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || size != opened.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return playbackReleaseFile{}, retentionConflict()
	}
	return playbackReleaseFile{filepath.Base(path), size, hex.EncodeToString(hash.Sum(nil))}, nil
}
func releasePlanValid(plan PlaybackReleasePlan, input PlaybackReleaseRequest) bool {
	if plan.Version != 1 || plan.ID != input.ID || plan.RefKind != input.ExpectedRefKind || plan.RefSHA256 != input.ExpectedRefSHA256 || !operationIDPattern.MatchString(plan.ReleaseID) || (plan.State != "prepared" && plan.State != "released") || len(plan.Files) > 32 {
		return false
	}
	names := map[string]bool{}
	for _, file := range plan.Files {
		if !releaseFileAllowed(file.Name) || names[file.Name] || !playbackDigestPattern.MatchString(file.SHA256) || file.Size < 0 || file.Size > releaseFileMaximum(file.Name) {
			return false
		}
		names[file.Name] = true
	}
	return true
}
func (service *Service) PreparePlaybackRelease(ctx context.Context, input PlaybackReleaseRequest) (PlaybackReleaseResult, error) {
	if !releaseRequestValid(input) {
		return PlaybackReleaseResult{}, contract.InputInvalid("视频释放参数无效")
	}
	entered := false
	safeNoMutation := false
	wrote := false
	value, err := service.withPlaybackLease(ctx, input.ID, func(ctx context.Context) (any, error) {
		entered = true
		if err := retentionMu.Lock(ctx); err != nil {
			return nil, err
		}
		defer retentionMu.Unlock()
		directory := filepath.Join(service.Root, input.ID)
		index, indexPath, err := service.retentionIndex(ctx, false)
		if err != nil {
			return nil, err
		}
		entry, exists := index.Entries[input.ID]
		// An interrupted prepare may leave no task directory. Recreate only an exact
		// indexed UUID directory, never source media or subtitle output.
		if _, err := os.Lstat(directory); os.IsNotExist(err) && exists {
			safeNoMutation = false
			wrote = true
			if err := os.Mkdir(directory, 0700); err != nil {
				return nil, err
			}
		}
		if _, statErr := os.Lstat(directory); os.IsNotExist(statErr) && !exists {
			safeNoMutation = true
			return nil, contract.NotFound("没有该视频留存记录")
		}
		if _, err := playbackPath(directory, true); err != nil {
			return nil, err
		}
		path := filepath.Join(directory, "playback-release.json")
		var plan PlaybackReleasePlan
		ref, err := retentionRecord(ctx, path, true, &plan)
		if err != nil {
			return nil, err
		}
		if ref != "" {
			if !releasePlanValid(plan, input) || (exists && (!releaseEntryMatches(entry, plan.RefKind, plan.RefSHA256) || !releasePlanMediaAllowed(plan, entry))) || (!exists && plan.State != "released") {
				return nil, retentionConflict()
			}
			// A restarted caller supplies a fresh candidate UUID; the durable plan owns
			// the original releaseID and is returned unchanged.
			return releaseResult(ctx, path, plan)
		}
		_, stagingErr := os.Lstat(filepath.Join(directory, ".playback-release-preparing.json"))
		safeNoMutation = os.IsNotExist(stagingErr) // Staging itself is a durable dispatch fence, including partial writes.
		if !exists || !releaseEntryMatches(entry, input.ExpectedRefKind, input.ExpectedRefSHA256) {
			return nil, retentionConflict()
		}

		files, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		if len(files) > 32 {
			return nil, retentionConflict()
		}
		plan = PlaybackReleasePlan{Version: 1, ID: input.ID, RefKind: input.ExpectedRefKind, RefSHA256: input.ExpectedRefSHA256, ReleaseID: input.ReleaseID, State: "prepared", Files: []playbackReleaseFile{}}
		for _, file := range files {
			if file.Name() == ".playback-release-preparing.json" {
				if info, err := playbackPath(filepath.Join(directory, file.Name()), false); err != nil || info.Size() > 65536 {
					return nil, retentionConflict()
				}
				continue
			}
			if !releaseFileAllowed(file.Name()) {
				return nil, retentionConflict()
			}
			maximum := releaseFileMaximum(file.Name())
			identity, err := releaseDigest(ctx, filepath.Join(directory, file.Name()), maximum)
			if err != nil {
				return nil, err
			}
			if !releaseMediaIdentityAllowed(identity, entry) {
				return nil, retentionConflict()
			}
			plan.Files = append(plan.Files, identity)
		}
		sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Name < plan.Files[j].Name })
		safeNoMutation = false
		wrote = true
		if err := saveReleasePlan(path, plan); err != nil {
			return nil, err
		}
		retentionReached("release-plan")
		entry.State = "releasing"
		index.Entries[input.ID] = entry
		if err := saveTaskJSON(indexPath, index); err != nil {
			return nil, err
		}
		return releaseResult(ctx, path, plan)
	})
	if err != nil {
		var app *contract.AppError
		if errors.As(err, &app) && !wrote && (safeNoMutation || (!entered && app.Code == contract.CodeBusy)) {
			app.Data = map[string]bool{"playbackReleaseNoMutation": true}
		}
		return PlaybackReleaseResult{}, err
	}
	return value.(PlaybackReleaseResult), nil
}
func (service *Service) FinalizePlaybackRelease(ctx context.Context, input PlaybackReleaseRequest) (PlaybackReleaseResult, error) {
	if !releaseRequestValid(input) {
		return PlaybackReleaseResult{}, contract.InputInvalid("视频释放参数无效")
	}
	value, err := service.withPlaybackLease(ctx, input.ID, func(ctx context.Context) (any, error) {
		if err := retentionMu.Lock(ctx); err != nil {
			return nil, err
		}
		defer retentionMu.Unlock()
		directory := filepath.Join(service.Root, input.ID)
		if _, err := playbackPath(directory, true); err != nil {
			return nil, err
		}
		path := filepath.Join(directory, "playback-release.json")
		var plan PlaybackReleasePlan
		ref, err := retentionRecord(ctx, path, false, &plan)
		if err != nil {
			return nil, err
		}
		if !playbackDigestPattern.MatchString(input.ExpectedTombstoneSHA256) || ref != input.ExpectedTombstoneSHA256 {
			return nil, retentionConflict()
		}
		if !releasePlanValid(plan, input) || plan.ReleaseID != input.ReleaseID {
			return nil, retentionConflict()
		}
		index, indexPath, err := service.retentionIndex(ctx, false)
		if err != nil {
			return nil, err
		}
		entry, exists := index.Entries[input.ID]
		if exists {
			if !releaseEntryMatches(entry, plan.RefKind, plan.RefSHA256) || !releasePlanMediaAllowed(plan, entry) {
				return nil, retentionConflict()
			}
		} else if plan.State != "released" {
			return nil, retentionConflict()
		}

		expected := map[string]playbackReleaseFile{}
		for _, file := range plan.Files {
			expected[file.Name] = file
		}
		files, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		// Preflight the complete remaining directory before deleting any file.
		for _, file := range files {
			if file.Name() == "playback-release.json" {
				continue
			}
			if file.Name() == ".playback-release-preparing.json" {
				if info, err := playbackPath(filepath.Join(directory, file.Name()), false); err != nil || info.Size() > 65536 {
					return nil, retentionConflict()
				}
				continue
			}
			wanted, ok := expected[file.Name()]
			if !ok || plan.State == "released" {
				return nil, retentionConflict()
			}
			actual, err := releaseDigest(ctx, filepath.Join(directory, file.Name()), wanted.Size)
			if err != nil {
				return nil, err
			}
			if actual != wanted {
				return nil, retentionConflict()
			}
		}
		if err := os.Remove(filepath.Join(directory, ".playback-release-preparing.json")); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, file := range plan.Files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			target := filepath.Join(directory, file.Name)
			if _, err := os.Lstat(target); os.IsNotExist(err) {
				continue
			}
			actual, err := releaseDigest(ctx, target, file.Size)
			if err != nil {
				return nil, err
			}
			if actual != file {
				return nil, retentionConflict()
			}
			if err := os.Remove(target); err != nil {
				return nil, err
			}
			retentionReached("release-delete-one")
		}
		plan.State = "released"
		if err := saveReleasePlan(path, plan); err != nil {
			return nil, err
		}
		retentionReached("release-released")
		if exists {
			delete(index.Entries, input.ID)
			if err := saveTaskJSON(indexPath, index); err != nil {
				return nil, err
			}
		}
		retentionReached("release-index-removed")
		return releaseResult(ctx, path, plan)
	})
	if err != nil {
		return PlaybackReleaseResult{}, err
	}
	return value.(PlaybackReleaseResult), nil
}

// The fixed staging name is itself a dispatch fence. A crashed partial staging
// file is recoverable only through prepare with the exact indexed reference.
func saveReleasePlan(path string, plan PlaybackReleasePlan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	staging := filepath.Join(filepath.Dir(path), ".playback-release-preparing.json")
	if _, err := os.Lstat(staging); err == nil {
		if _, err := playbackPath(staging, false); err != nil {
			return err
		}
		if err := os.Remove(staging); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(staging, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	retentionReached("release-staging-written")
	return os.Rename(staging, path)
}

func releaseResult(ctx context.Context, path string, plan PlaybackReleasePlan) (PlaybackReleaseResult, error) {
	var stored PlaybackReleasePlan
	ref, err := retentionRecord(ctx, path, false, &stored)
	if err != nil || !releasePlanEqual(plan, stored) {
		return PlaybackReleaseResult{}, retentionConflict()
	}
	return PlaybackReleaseResult{plan, ref}, nil
}
func releasePlanEqual(a, b PlaybackReleasePlan) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func (service *Service) releasedTaskProjection(ctx context.Context, id string) bool {
	var plan PlaybackReleasePlan
	ref, err := retentionRecord(ctx, filepath.Join(service.Root, id, "playback-release.json"), true, &plan)
	if err != nil || ref == "" || plan.State != "released" {
		return false
	}
	input := PlaybackReleaseRequest{ID: id, ReleaseID: plan.ReleaseID, ExpectedRefKind: plan.RefKind, ExpectedRefSHA256: plan.RefSHA256}
	return releasePlanValid(plan, input)
}
