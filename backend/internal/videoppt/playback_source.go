package videoppt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

var playbackDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// PlaybackDeclaration is metadata only. The shell must verify its own open handle.
// No source path, remote address or user metadata crosses this private interface.
type PlaybackDeclaration struct {
	Version         int    `json:"version"`
	Verification    string `json:"verification"`
	TaskID          string `json:"taskID"`
	SourceKind      string `json:"sourceKind"`
	SourceIdentity  string `json:"sourceIdentity"`
	SourceSHA256    string `json:"sourceSHA256"`
	Size            int64  `json:"size"`
	BindingRevision string `json:"bindingRevision"`
}

func playbackUnavailable() error {
	return contract.Unavailable("托管视频来源不可用，请保留任务并核对来源")
}

func playbackPath(path string, directory bool) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return nil, playbackUnavailable()
	}
	resolved, err := filepath.EvalSymlinks(path)
	samePath := filepath.Clean(resolved) == filepath.Clean(path)
	if runtime.GOOS == "windows" {
		samePath = strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path))
	}
	if err != nil || !samePath {
		return nil, playbackUnavailable()
	}
	return info, nil
}

func playbackMetadata(ctx context.Context, path string, optional bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) && optional {
		return nil, nil
	}
	before, err := playbackPath(path, false)
	if err != nil || before.Size() > 64*1024 {
		return nil, playbackUnavailable()
	}
	input, err := os.Open(path)
	if err != nil {
		return nil, playbackUnavailable()
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, playbackUnavailable()
	}
	body, err := io.ReadAll(io.LimitReader(input, 64*1024+1))
	if err != nil || len(body) > 64*1024 {
		return nil, playbackUnavailable()
	}
	after, err := playbackPath(path, false)
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, playbackUnavailable()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}

// PlaybackSource never calls MediaSource/VideoDigest and never reads video bytes.
// Existing local tasks legitimately lack media-source.json; its presence is fenced.
func (service *Service) PlaybackSource(ctx context.Context, id, expectedSHA256 string) (PlaybackDeclaration, error) {
	empty := PlaybackDeclaration{}
	if !operationIDPattern.MatchString(id) || !playbackDigestPattern.MatchString(expectedSHA256) {
		return empty, contract.InputInvalid("视频播放参数无效")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := service.playbackRoot(); err != nil {
		return empty, playbackUnavailable()
	}
	root, err := filepath.Abs(service.Root)
	if err != nil {
		return empty, playbackUnavailable()
	}
	if _, err := playbackPath(root, true); err != nil {
		return empty, err
	}
	if err := service.CheckPlaybackDispatch(ctx, id); err != nil {
		return empty, err
	}
	directory := filepath.Join(root, id)
	if _, err := playbackPath(directory, true); err != nil {
		return empty, err
	}
	operationBody, err := playbackMetadata(ctx, filepath.Join(directory, "operation.json"), false)
	if err != nil {
		return empty, err
	}
	var operation VideoOperation
	if json.Unmarshal(operationBody, &operation) != nil || operation.Version != 1 {
		return empty, playbackUnavailable()
	}
	switch operation.Stage {
	case "source", "auto", "subtitle", "subtitle-station":
	default:
		return empty, playbackUnavailable()
	}
	source, err := operationSource(operation)
	if err != nil {
		return empty, playbackUnavailable()
	}
	metadataBody, err := playbackMetadata(ctx, filepath.Join(directory, "media-source.json"), source.Kind == "local")
	if err != nil {
		return empty, err
	}
	if metadataBody != nil {
		var resolved contract.MediaSource
		if json.Unmarshal(metadataBody, &resolved) != nil || !resolved.Valid() || resolved.Kind != source.Kind || resolved.URL != source.URL || resolved.VideoID != source.VideoID || resolved.Page != source.Page || resolved.Path != source.Path || (source.Kind == "local" && resolved.SHA256 != operation.SourceHash) {
			return empty, playbackUnavailable()
		}
		source = resolved
	}
	if source.Kind == "bili" && source.Status != "resolved" {
		return empty, playbackUnavailable()
	}
	readyBody, err := playbackMetadata(ctx, filepath.Join(directory, "video-ready.json"), false)
	if err != nil {
		return empty, err
	}
	var ready struct {
		Version int    `json:"version"`
		SHA256  string `json:"sha256"`
	}
	if json.Unmarshal(readyBody, &ready) != nil || ready.Version != 1 || !playbackDigestPattern.MatchString(ready.SHA256) {
		return empty, playbackUnavailable()
	}
	if ready.SHA256 != expectedSHA256 || (source.Kind == "local" && ready.SHA256 != operation.SourceHash) {
		return empty, contract.NewError(contract.CodeConflict, "托管视频绑定已经改变")
	}
	info, err := playbackPath(filepath.Join(directory, videoFileName), false)
	if err != nil {
		return empty, err
	}
	if info.Size() <= 0 || info.Size() > 8*1024*1024*1024 {
		return empty, contract.Unavailable("媒体为空或超过8GiB播放上限")
	}
	identity := "sha256:" + ready.SHA256
	if source.Kind == "bili" {
		identity = source.BVID + ":" + strconv.FormatInt(source.CID, 10) + ":" + strconv.Itoa(source.Page)
	}
	// Length-delimited JSON distinguishes absent optional metadata from any byte sequence.
	fenceBody, _ := json.Marshal([][]byte{operationBody, metadataBody, readyBody})
	fence := sha256.Sum256(fenceBody)
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return PlaybackDeclaration{1, "declared", id, source.Kind, identity, ready.SHA256, info.Size(), hex.EncodeToString(fence[:])}, nil
}

// Reject unsupported roots before any filesystem operation in playback lifecycles.
func (service *Service) playbackRoot() error {
	if !filepath.IsAbs(service.Root) || (runtime.GOOS == "windows" && (strings.HasPrefix(service.Root, `\\`) || (len(service.Root) > 2 && strings.Contains(service.Root[2:], ":")))) {
		return playbackUnavailable()
	}
	return nil
}
