package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type TransferResult struct {
	Path          string   `json:"path"`
	Size          int64    `json:"size"`
	Hash          string   `json:"hash"`
	ETag          string   `json:"etag"`
	Total         *int64   `json:"total"`
	Intermediates []string `json:"intermediates,omitempty"`
	Warning       string   `json:"warning,omitempty"`
}

type checkpointJournal struct {
	Version  int    `json:"version"`
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	Hash     string `json:"hash"`
	ETag     string `json:"etag"`
	Total    *int64 `json:"total"`
}

var transferKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var strongETagPattern = regexp.MustCompile(`^"[^"\r\n]+"$`)
var contentRangePattern = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+)$`)
var lengthPattern = regexp.MustCompile(`^\d+$`)

func hashFileStreaming(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("下载成果文件不可用")
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("下载成果文件不可用")
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func strongETag(value string) string {
	if value != "" && strongETagPattern.MatchString(value) {
		return value
	}
	return ""
}

type TransferInput struct {
	Directory string
	Key       string
	Identity  string
	URL       string
	Headers   map[string]string
	MaxBytes  int64
	Buffer    int
	Progress  func(downloaded int64, total *int64)
	Fetch     func(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error)
	Ctx       context.Context // NOSONAR: request-scoped input struct; the caller owns this context for a single operation.
}

func (input *TransferInput) cancelled() error {
	select {
	case <-input.Ctx.Done():
		return contract.NewError(contract.CodeCancelled, "下载已取消")
	default:
		return nil
	}
}

func (input *TransferInput) fetcher(requestURL string, headers map[string]string) (*http.Response, error) {
	if input.Fetch != nil {
		return input.Fetch(requestURL, headers, input.Ctx)
	}
	return nil, fmt.Errorf("传输层未配置")
}

func validateTransferDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("下载任务目录无效")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || strings.ToLower(resolved) != strings.ToLower(directory) {
		return fmt.Errorf("下载任务目录无效")
	}
	return nil
}

func TransferMedia(input TransferInput) (*TransferResult, error) {
	if !transferKeyPattern.MatchString(input.Key) || input.Identity == "" || len(input.Identity) > 500 {
		return nil, fmt.Errorf("下载分段标识无效")
	}
	writeBuffer := int64(input.Buffer)
	if writeBuffer == 0 {
		writeBuffer = 65536
	}
	if writeBuffer < 1024 || writeBuffer > 10485760 {
		return nil, fmt.Errorf("下载写入缓冲区大小无效")
	}
	if err := input.cancelled(); err != nil {
		return nil, err
	}
	directory := input.Directory
	if resolved, err := filepath.EvalSymlinks(directory); err == nil {
		directory = resolved
	}
	if err := validateTransferDirectory(directory); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, input.Key+".partial")
	journal := filepath.Join(directory, input.Key+".resume.json")
	lock := filepath.Join(directory, input.Key+".lock")
	for _, file := range []string{path, journal, lock} {
		if info, err := os.Lstat(file); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return nil, fmt.Errorf("下载分段文件无效")
		}
	}
	limit := input.MaxBytes
	if limit == 0 {
		limit = 50 * 1024 * 1024 * 1024
	}
	lockFile, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("下载分段锁被占用")
	}
	defer os.Remove(lock)
	if _, err := lockFile.Write([]byte(fmt.Sprintf(`{"version":1,"pid":%d,"identity":%q}`, os.Getpid(), input.Identity))); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("下载分段锁写入失败")
	}
	if err := lockFile.Sync(); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("下载分段锁写入失败")
	}
	if err := lockFile.Close(); err != nil {
		return nil, fmt.Errorf("下载分段锁写入失败")
	}

	size := int64(0)
	total := (*int64)(nil)
	etag := ""
	if journalRaw, err := os.ReadFile(journal); err == nil {
		var saved checkpointJournal
		if json.Unmarshal(journalRaw, &saved) == nil {
			fileInfo, statErr := os.Lstat(path)
			if saved.Identity == input.Identity && statErr == nil && fileInfo.Size() == saved.Size &&
				saved.ETag != "" && saved.Size > 0 && saved.Size <= limit {
				if hash, _, hashErr := hashFileStreaming(path); hashErr == nil && hash == saved.Hash {
					size = saved.Size
					total = saved.Total
					etag = saved.ETag
				}
			}
		}
	} else if input.cancelled() != nil {
		return nil, input.cancelled()
	}

	headers := map[string]string{"Accept-Encoding": "identity"}
	for key, value := range input.Headers {
		headers[key] = value
	}
	if size > 0 {
		headers["Range"] = "bytes=" + strconv.FormatInt(size, 10) + "-"
		headers["If-Range"] = etag
	}
	response, err := input.fetcher(input.URL, headers)
	if err != nil {
		return nil, err
	}
	if response.Body != nil {
		defer response.Body.Close()
	}
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable && size > 0 &&
		total != nil && *total == size &&
		response.Header.Get("Content-Range") == "bytes */"+strconv.FormatInt(size, 10) &&
		strongETag(response.Header.Get("ETag")) == etag {
		hash, _, _ := hashFileStreaming(path)
		return &TransferResult{Path: path, Size: size, Hash: hash, ETag: etag, Total: total}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || response.Body == nil {
		return nil, fmt.Errorf("媒体下载未完成（HTTP %d）", response.StatusCode)
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, fmt.Errorf("媒体响应压缩方式不支持断点校验")
	}
	incomingTag := strongETag(response.Header.Get("ETag"))
	lengthHeader := response.Header.Get("Content-Length")
	length := (*int64)(nil)
	if lengthHeader != "" && lengthPattern.MatchString(lengthHeader) {
		parsed, _ := strconv.ParseInt(lengthHeader, 10, 64)
		length = &parsed
	}
	if size > 0 && response.StatusCode == http.StatusPartialContent {
		range_ := contentRangePattern.FindStringSubmatch(response.Header.Get("Content-Range"))
		if range_ == nil {
			return nil, fmt.Errorf("断点响应与原分段不一致，请重新建立下载")
		}
		rangeStart, _ := strconv.ParseInt(range_[1], 10, 64)
		rangeEnd, _ := strconv.ParseInt(range_[2], 10, 64)
		rangeTotal, _ := strconv.ParseInt(range_[3], 10, 64)
		if rangeStart != size || rangeEnd < size || rangeTotal <= rangeEnd || incomingTag != etag ||
			(total != nil && rangeTotal != *total) ||
			(length != nil && *length != rangeEnd-size+1) {
			return nil, fmt.Errorf("断点响应与原分段不一致，请重新建立下载")
		}
		total = &rangeTotal
	} else if response.StatusCode == http.StatusOK {
		size = 0
		total = length
		etag = incomingTag
	} else {
		return nil, fmt.Errorf("下载服务未正确响应断点请求")
	}
	if total != nil && *total > limit {
		return nil, fmt.Errorf("媒体大小超过此任务上限")
	}

	handle, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("下载写入失败")
	}
	writeOffset := size
	if writeOffset > 0 {
		if _, err := handle.Seek(writeOffset, io.SeekStart); err != nil {
			handle.Close()
			return nil, fmt.Errorf("下载写入失败")
		}
	} else if err := handle.Truncate(0); err != nil {
		handle.Close()
		return nil, fmt.Errorf("下载写入失败")
	}
	reader := response.Body
	var writeErr error
	func() {
		buffer := make([]byte, writeBuffer)
		for {
			if err := input.cancelled(); err != nil {
				writeErr = err
				return
			}
			read, readErr := reader.Read(buffer)
			if read > 0 {
				if size+int64(read) > limit || (total != nil && size+int64(read) > *total) {
					writeErr = fmt.Errorf("媒体响应超过声明长度")
					return
				}
				if _, err := handle.Write(buffer[:read]); err != nil {
					writeErr = fmt.Errorf("下载写入中断")
					return
				}
				size += int64(read)
				if input.Progress != nil {
					input.Progress(size, total)
				}
			}
			if readErr == io.EOF {
				return
			}
			if readErr != nil {
				writeErr = readErr
				return
			}
		}
	}()
	if writeErr == nil && (total != nil && size != *total || size == 0) {
		writeErr = fmt.Errorf("媒体响应不完整，恢复文件已保留")
	}
	if writeErr != nil {
		if hash, fileSize, hashErr := hashFileStreaming(path); hashErr == nil && fileSize > 0 {
			_ = saveCheckpoint(journal, checkpointJournal{Version: 1, Identity: input.Identity, Size: fileSize, Hash: hash, ETag: etag, Total: total})
		}
		handle.Close()
		return nil, writeErr
	}
	if err := handle.Sync(); err != nil {
		handle.Close()
		return nil, fmt.Errorf("下载写入中断")
	}
	if err := handle.Close(); err != nil {
		return nil, fmt.Errorf("下载写入中断")
	}
	hash, _, err := hashFileStreaming(path)
	if err != nil {
		return nil, err
	}
	if err := saveCheckpoint(journal, checkpointJournal{Version: 1, Identity: input.Identity, Size: size, Hash: hash, ETag: etag, Total: total}); err != nil {
		return nil, err
	}
	return &TransferResult{Path: path, Size: size, Hash: hash, ETag: etag, Total: total}, nil
}

func saveCheckpoint(journal string, value checkpointJournal) error {
	tmp := journal + ".writing"
	if info, err := os.Lstat(tmp); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("下载恢复记录路径无效")
	}
	encoded, _ := json.Marshal(value)
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, journal)
}
