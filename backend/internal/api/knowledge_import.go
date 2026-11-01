package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
)

const (
	knowledgeImportMaxBytes = 100_000_000
	knowledgeTextMaxBytes   = 6_000_000
	knowledgeTextMaxRunes   = 2_000_000
	knowledgeImportMaxFiles = 200
	knowledgePickerTimeout  = 24 * 60 * 60 * 1000
)

var knowledgeImportExtensions = []string{"pdf", "docx", "txt", "md", "srt"}

var (
	errKnowledgeFileTooLarge   = errors.New("资料超过 100 MB，请按章节导入")
	errKnowledgeTextTooLarge   = errors.New("文本资料超过 6 MB，请拆分后导入")
	errKnowledgeTextTooLong    = errors.New("资料文字超过 2,000,000 字，请拆分后导入")
	errKnowledgeTooManyFiles   = errors.New("一次最多导入 200 份资料")
	errKnowledgeFileType       = errors.New("资料只支持 PDF、Word（DOCX）、TXT、Markdown 和 SRT")
	errKnowledgeNeedsOcr       = errors.New("这份 PDF 主要是图片，请先识别文字后再导入知识库")
	errKnowledgeFileChanged    = errors.New("导入期间资料已改变，请重新选择")
	errKnowledgeFileUnreadable = errors.New("资料文件不可读")
)

// knowledgeImportItem 是“导入资料”按钮逐个文件的结果，字段与渲染层约定一致。
type knowledgeImportItem struct {
	Name   string         `json:"name"`
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

// handleKnowledgeImport 支持两种调用：
//   - 带 title/text 的完整 ImportInput：直接写入（内部与测试使用）；
//   - 只带 category：经壳侧 shell.pickFiles 选择本地资料，逐个解析导入并返回逐文件结果。
func (inv *Invoker) handleKnowledgeImport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var probe map[string]json.RawMessage
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &probe); err != nil {
			return nil, contract.InputInvalid("资料导入参数无效")
		}
	}
	_, hasTitle := probe["title"]
	_, hasText := probe["text"]
	if hasTitle || hasText {
		return inv.importKnowledgeInput(state, params)
	}
	var input struct {
		Category string `json:"category"`
	}
	if len(probe) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, contract.InputInvalid("资料导入参数无效")
		}
	}
	category := strings.TrimSpace(input.Category)
	if len([]rune(category)) > 100 {
		return nil, contract.InputInvalid("资料分类无效")
	}
	if inv.jobs.Current() != nil {
		return nil, contract.NewError(contract.CodeBusy, "当前已有任务运行，请等待完成或取消")
	}
	paths, err := pickKnowledgeFiles(store, state)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return []knowledgeImportItem{}, nil
	}
	if len(paths) > knowledgeImportMaxFiles {
		return nil, contract.InputInvalid(errKnowledgeTooManyFiles.Error())
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	var caller documents.NativeCaller
	if inv.native != nil {
		caller = inv.native
	}
	return inv.runJob("知识库导入", func(ctx context.Context) (any, error) {
		results := make([]knowledgeImportItem, 0, len(paths))
		for index, path := range paths {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("知识库导入已取消: %w", err)
			}
			inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
				"stage": "正在导入知识库资料", "message": fmt.Sprintf("%d/%d %s", index+1, len(paths), filepath.Base(path)),
			}})
			results = append(results, importKnowledgeFile(service, caller, path, category))
		}
		return results, nil
	})
}

func (inv *Invoker) importKnowledgeInput(state *contract.State, params json.RawMessage) (any, error) {
	var input knowledge.ImportInput
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("资料导入参数无效")
	}
	service, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	result, err := service.Import(input)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return result, nil
}

func pickKnowledgeFiles(store contract.StateStore, state *contract.State) ([]string, error) {
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	params := map[string]any{"title": "导入知识库资料", "extensions": knowledgeImportExtensions}
	if state.LibraryRoot != "" {
		params["defaultPath"] = state.LibraryRoot
	}
	raw, err := reverse.Call("shell.pickFiles", params, knowledgePickerTimeout)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Paths []string `json:"paths"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("文件选择器响应无效")
	}
	paths := make([]string, 0, len(picked.Paths))
	for _, path := range picked.Paths {
		if strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// importKnowledgeFile 读取一份本地资料并写入知识库；任何错误都落在本文件的结果里，不中断其余文件。
// 与 0.1.85 导入的数据保持一致：标题为文件名，来源地址为绝对路径，sourceHash 为原文件字节的 SHA-256。
func importKnowledgeFile(service *knowledge.Store, caller documents.NativeCaller, path, category string) knowledgeImportItem {
	item := knowledgeImportItem{Name: filepath.Base(path)}
	input, err := readKnowledgeFile(caller, path)
	if err == nil {
		input.Category = category
		item.Result, err = service.Import(input)
	}
	if err != nil {
		item.Error = err.Error()
		return item
	}
	item.OK = true
	return item
}

func readKnowledgeFile(caller documents.NativeCaller, path string) (knowledge.ImportInput, error) {
	locator, err := filepath.Abs(path)
	if err != nil {
		return knowledge.ImportInput{}, fmt.Errorf("资料路径无效: %w", err)
	}
	info, err := os.Stat(locator)
	if err != nil {
		return knowledge.ImportInput{}, fmt.Errorf("%w: %w", errKnowledgeFileUnreadable, err)
	}
	if !info.Mode().IsRegular() {
		return knowledge.ImportInput{}, errKnowledgeFileUnreadable
	}
	extension := strings.ToLower(filepath.Ext(locator))
	if !knowledgeExtensionAllowed(extension) {
		return knowledge.ImportInput{}, errKnowledgeFileType
	}
	if info.Size() > knowledgeImportMaxBytes {
		return knowledge.ImportInput{}, errKnowledgeFileTooLarge
	}
	textLike := extension == ".txt" || extension == ".md" || extension == ".srt"
	if textLike && info.Size() > knowledgeTextMaxBytes {
		return knowledge.ImportInput{}, errKnowledgeTextTooLarge
	}
	data, err := os.ReadFile(locator)
	if err != nil {
		return knowledge.ImportInput{}, fmt.Errorf("%w: %w", errKnowledgeFileUnreadable, err)
	}
	sum := sha256.Sum256(data)
	text, err := knowledgeFileText(caller, locator, data)
	if err != nil {
		return knowledge.ImportInput{}, err
	}
	if len([]rune(text)) > knowledgeTextMaxRunes {
		return knowledge.ImportInput{}, errKnowledgeTextTooLong
	}
	again, err := os.Open(locator)
	if err != nil {
		return knowledge.ImportInput{}, fmt.Errorf("%w: %w", errKnowledgeFileUnreadable, err)
	}
	defer again.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(again, knowledgeImportMaxBytes+1)); err != nil {
		return knowledge.ImportInput{}, fmt.Errorf("%w: %w", errKnowledgeFileUnreadable, err)
	}
	if !bytes.Equal(hasher.Sum(nil), sum[:]) {
		return knowledge.ImportInput{}, errKnowledgeFileChanged
	}
	return knowledge.ImportInput{
		Title: filepath.Base(locator), Text: text, Locator: locator,
		SourceHash: hex.EncodeToString(sum[:]), Origin: "local",
	}, nil
}

func knowledgeExtensionAllowed(extension string) bool {
	for _, allowed := range knowledgeImportExtensions {
		if extension == "."+allowed {
			return true
		}
	}
	return false
}

func knowledgeFileText(caller documents.NativeCaller, path string, data []byte) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".srt":
		return documents.DecodeTextFile(data), nil
	case ".docx":
		text, err := documents.ParseDocx(data)
		if err != nil {
			return "", fmt.Errorf("Word 资料解析失败: %w", err)
		}
		return text, nil
	case ".pdf":
		parsed, err := documents.ParseDocument(caller, path)
		if err != nil {
			return "", fmt.Errorf("PDF 资料解析失败: %w", err)
		}
		if parsed.NeedsOcr {
			return "", errKnowledgeNeedsOcr
		}
		return parsed.Text, nil
	}
	return "", errKnowledgeFileType
}
