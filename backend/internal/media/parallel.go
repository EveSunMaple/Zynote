package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const headerContentRange = "Content-Range"

type ParallelInput struct {
	TransferInput
	Concurrency int
	ChunkBytes  int64
	Head        func(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error)
}

type chunkRange struct {
	start int64
	end   int64
	key   string
}

func (input *ParallelInput) serial(intermediates []string, warning string) (*TransferResult, error) {
	result, err := TransferMedia(input.TransferInput)
	if err != nil {
		return nil, err
	}
	result.Intermediates = intermediates
	result.Warning = warning
	return result, nil
}

type rangeSetup struct {
	headers       map[string]string
	size          int64
	etag          string
	ranges        []chunkRange
	intermediates []string
	maxBytes      int64
}

func prepareRangeSetup(input ParallelInput, concurrency int, chunkBytes, maxBytes int64) (*rangeSetup, string, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	headers := map[string]string{"Accept-Encoding": "identity"}
	for key, value := range input.Headers {
		headers[key] = value
	}
	delete(headers, "Range")
	delete(headers, "If-Range")
	head := input.Head
	if head == nil {
		return nil, "未取得并行范围，已使用串行下载", nil
	}
	headCtx, cancelHead := context.WithTimeout(input.Ctx, 20*time.Second)
	response, err := head(input.URL, headers, headCtx)
	cancelHead()
	if err != nil {
		if input.Ctx.Err() != nil {
			return nil, "", contract.NewError(contract.CodeCancelled, "下载已取消")
		}
		return nil, "未取得并行范围，已使用串行下载", nil
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}
	size := int64(0)
	if raw := response.Header.Get("Content-Length"); lengthPattern.MatchString(raw) {
		size, _ = strconv.ParseInt(raw, 10, 64)
	}
	etag := response.Header.Get("ETag")
	minSize := int64(concurrency * 65536)
	if chunkBytes > 0 {
		minSize = 2
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || size < minSize ||
		!strongETagPattern.MatchString(etag) ||
		strings.ToLower(response.Header.Get("Accept-Ranges")) != "bytes" {
		return nil, "服务器未提供可核验的分段范围，已使用完整串行下载", nil
	}
	if size > maxBytes {
		return nil, "", fmt.Errorf("媒体大小超过此任务上限")
	}
	count := int64(concurrency)
	if chunkBytes > 0 {
		count = (size + chunkBytes - 1) / chunkBytes
	}
	if count > 4096 {
		return nil, "", fmt.Errorf("HTTP 分段超过 4096 份，请增加分段大小")
	}
	ranges := make([]chunkRange, count)
	for index := int64(0); index < count; index++ {
		start := int64(0)
		end := int64(0)
		if chunkBytes > 0 {
			start = index * chunkBytes
			end = min64(size, start+chunkBytes) - 1
		} else {
			start = size * index / count
			end = size*(index+1)/count - 1
		}
		ranges[index] = chunkRange{start: start, end: end, key: fmt.Sprintf("%s-chunk-%d", input.Key, index)}
	}
	intermediates := []string{}
	for _, chunk := range ranges {
		intermediates = append(intermediates, chunk.key+".partial", chunk.key+".resume.json", chunk.key+".resume.json.writing")
	}
	return &rangeSetup{
		headers: headers, size: size, etag: etag, ranges: ranges,
		intermediates: intermediates, maxBytes: maxBytes,
	}, "", nil
}

func rangeFetcher(input ParallelInput, chunk chunkRange, total, size int64, etag string, ignored *atomic.Bool) func(string, map[string]string, context.Context) (*http.Response, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	return func(requestURL string, requestHeaders map[string]string, requestCtx context.Context) (*http.Response, error) {
		prefix := int64(0)
		if raw := requestHeaders["Range"]; raw != "" {
			if strings.HasPrefix(raw, "bytes=") {
				value := strings.TrimSuffix(strings.TrimPrefix(raw, "bytes="), "-")
				prefix, _ = strconv.ParseInt(value, 10, 64)
			}
		}
		if prefix == total {
			return &http.Response{
				StatusCode: http.StatusRequestedRangeNotSatisfiable,
				Header: http.Header{
					"Etag":             []string{etag},
					headerContentRange: []string{fmt.Sprintf("bytes */%d", total)},
				},
				Body: nil,
			}, nil
		}
		requestHeaders["Range"] = fmt.Sprintf("bytes=%d-%d", chunk.start+prefix, chunk.end)
		requestHeaders["If-Range"] = etag
		response, err := input.Fetch(requestURL, requestHeaders, requestCtx)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusOK {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			ignored.Store(true)
			return nil, fmt.Errorf("服务器忽略并行范围")
		}
		match := contentRangePattern.FindStringSubmatch(response.Header.Get(headerContentRange))
		valid := response.StatusCode == http.StatusPartialContent && match != nil
		if valid {
			rangeStart, _ := strconv.ParseInt(match[1], 10, 64)
			rangeEnd, _ := strconv.ParseInt(match[2], 10, 64)
			rangeTotal, _ := strconv.ParseInt(match[3], 10, 64)
			valid = rangeStart == chunk.start+prefix && rangeEnd == chunk.end && rangeTotal == size &&
				response.Header.Get("ETag") == etag
		}
		if !valid {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			return nil, fmt.Errorf("并行响应或媒体版本不一致，保留恢复文件")
		}
		response.Header.Set("Content-Length", strconv.FormatInt(total-prefix, 10))
		response.Header.Set(headerContentRange, fmt.Sprintf("bytes %d-%d/%d", prefix, total-1, total))
		response.StatusCode = http.StatusOK
		if prefix > 0 {
			response.StatusCode = http.StatusPartialContent
		}
		return response, nil
	}
}

func runParallelChunks(input ParallelInput, setup *rangeSetup, concurrency int, buffer int64) ([]*TransferResult, bool, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	ctx, cancel := context.WithCancel(input.Ctx)
	defer cancel()
	count := int64(len(setup.ranges))
	results := make([]*TransferResult, count)
	failures := make([]error, count)
	progress := make([]int64, count)
	var ignored atomic.Bool
	var next int64 = -1
	var mu sync.Mutex
	var firstErr error
	workers := concurrency
	if int64(workers) > count {
		workers = int(count)
	}
	download := func(index int64) (*TransferResult, error) {
		chunk := setup.ranges[index]
		total := chunk.end - chunk.start + 1
		return TransferMedia(TransferInput{
			Directory: input.Directory,
			Key:       chunk.key,
			Identity:  fmt.Sprintf("%s:range:%d-%d", input.Identity, chunk.start, chunk.end),
			URL:       input.URL,
			Headers:   input.Headers,
			MaxBytes:  setup.maxBytes,
			Buffer:    int(buffer),
			Ctx:       ctx,
			Fetch:     rangeFetcher(input, chunk, total, setup.size, setup.etag, &ignored),
			Progress: func(downloaded int64, _ *int64) {
				progress[index] = downloaded
				if input.Progress != nil {
					sum := int64(0)
					for _, value := range progress {
						sum += value
					}
					total := setup.size
					input.Progress(sum, &total)
				}
			},
		})
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				next++
				index := next
				mu.Unlock()
				if index >= count {
					return
				}
				result, err := download(index)
				results[index] = result
				failures[index] = err
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
			}
		}()
	}
	wg.Wait()
	if input.Ctx.Err() != nil {
		return nil, false, contract.NewError(contract.CodeCancelled, "下载已取消")
	}
	if firstErr != nil {
		return nil, ignored.Load(), firstErr
	}
	for index := int64(0); index < count; index++ {
		if results[index] == nil {
			return nil, false, fmt.Errorf("下载分段未全部完成，恢复文件保留")
		}
	}
	return results, false, nil
}

func mergeParallelChunks(input ParallelInput, setup *rangeSetup, results []*TransferResult) (*TransferResult, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	path := filepath.Join(input.Directory, input.Key+".partial")
	journal := filepath.Join(input.Directory, input.Key+".resume.json")
	tmp := journal + ".writing"
	for _, target := range []string{path, journal, tmp} {
		if info, err := os.Lstat(target); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return nil, fmt.Errorf("并行合并目标无效")
		}
	}
	output, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("并行合并写入失败")
	}
	hash := sha256.New()
	written := int64(0)
	mergeErr := error(nil)
	func() {
		buffer := make([]byte, input.Buffer)
		for _, result := range results {
			reader, err := os.Open(result.Path)
			if err != nil {
				mergeErr = fmt.Errorf("并行片段未全部完成")
				return
			}
			for {
				read, readErr := reader.Read(buffer)
				if read > 0 {
					hash.Write(buffer[:read])
					if _, err := output.Write(buffer[:read]); err != nil {
						reader.Close()
						mergeErr = fmt.Errorf("合并分段写入中断")
						return
					}
					written += int64(read)
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					reader.Close()
					mergeErr = fmt.Errorf("并行片段读取失败")
					return
				}
			}
			reader.Close()
		}
		if written != setup.size {
			mergeErr = fmt.Errorf("并行片段合并长度不一致")
			return
		}
		if err := output.Sync(); err != nil {
			mergeErr = fmt.Errorf("合并分段写入中断")
		}
	}()
	if closeErr := output.Close(); closeErr != nil && mergeErr == nil {
		mergeErr = fmt.Errorf("合并分段写入中断")
	}
	if mergeErr != nil {
		return nil, mergeErr
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	total := setup.size
	if err := saveCheckpoint(journal, checkpointJournal{
		Version: 1, Identity: input.Identity, Size: setup.size, Hash: digest, ETag: setup.etag, Total: &total,
	}); err != nil {
		return nil, err
	}
	return &TransferResult{
		Path: path, Size: setup.size, Hash: digest, ETag: setup.etag, Total: &total,
		Intermediates: setup.intermediates, Warning: "",
	}, nil
}

func TransferMediaRanges(input ParallelInput) (*TransferResult, error) {
	concurrency := input.Concurrency
	if concurrency < 1 || concurrency > 16 {
		return nil, fmt.Errorf("并行片段数无效")
	}
	chunkBytes := input.ChunkBytes
	if chunkBytes < 0 || chunkBytes > 104857600 {
		return nil, fmt.Errorf("HTTP 分段大小无效")
	}
	buffer := input.Buffer
	if buffer == 0 {
		buffer = 65536
	}
	if buffer < 1024 || buffer > 10485760 {
		return nil, fmt.Errorf("下载写入缓冲区大小无效")
	}
	input.Buffer = buffer
	if concurrency == 1 && chunkBytes == 0 {
		return input.serial(nil, "")
	}
	maxBytes := input.MaxBytes
	if maxBytes == 0 {
		maxBytes = 50 * 1024 * 1024 * 1024
	}
	setup, warning, err := prepareRangeSetup(input, concurrency, chunkBytes, maxBytes)
	if err != nil {
		return nil, err
	}
	if setup == nil {
		return input.serial(nil, warning)
	}
	results, ignored, err := runParallelChunks(input, setup, concurrency, int64(buffer))
	if err != nil {
		if ignored {
			return input.serial(setup.intermediates, "服务器未执行并行范围，已切换串行下载")
		}
		return nil, err
	}
	return mergeParallelChunks(input, setup, results)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
