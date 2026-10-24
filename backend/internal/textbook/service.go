package textbook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

type Caller interface {
	Call(method string, params any, timeoutMS int) (json.RawMessage, error)
}

type book struct {
	path string
	hash string
}

type draft struct {
	bookID string
	hash   string
	rows   []Highlight
}

type Service struct {
	mu     sync.Mutex
	books  map[string]book
	drafts map[string]draft
	files  fileServer
}

func New() *Service {
	return &Service{books: map[string]book{}, drafts: map[string]draft{}}
}

type inspectResult struct {
	Pages    int         `json:"pages"`
	Sizes    [][]float64 `json:"sizes"`
	Previous []Highlight `json:"previous"`
}

func cleanCallerError(err error) string {
	message := err.Error()
	if index := strings.Index(message, ": "); index > 0 && message[:index] == strings.ToUpper(message[:index]) {
		return message[index+2:]
	}
	return message
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func inspect(caller Caller, path string) (inspectResult, error) {
	if caller == nil {
		return inspectResult{}, contract.Unavailable("PDF 解析组件不可用")
	}
	raw, err := caller.Call("pdf.inspect", map[string]any{"path": path}, 60000)
	if err != nil {
		return inspectResult{}, contract.Unavailable(cleanCallerError(err))
	}
	var result inspectResult
	if err := json.Unmarshal(raw, &result); err != nil || result.Pages < 1 {
		return inspectResult{}, contract.Unavailable("教材解析结果无效")
	}
	return result, nil
}

func (service *Service) Select(caller Caller, path string) (any, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, contract.InputInvalid("教材路径无效")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() || strings.ToLower(filepath.Ext(absolute)) != ".pdf" {
		return nil, contract.InputInvalid("请选择教材 PDF")
	}
	parsed, err := inspect(caller, absolute)
	if err != nil {
		return nil, err
	}
	hash, err := fileHash(absolute)
	if err != nil {
		return nil, contract.Unavailable("教材文件不可读")
	}
	id := ids.New()
	service.mu.Lock()
	service.books[id] = book{path: absolute, hash: hash}
	service.mu.Unlock()
	return map[string]any{"id": id, "path": absolute, "pages": parsed.Pages}, nil
}

func (service *Service) Bytes(id string) (any, error) {
	service.mu.Lock()
	item, ok := service.books[id]
	service.mu.Unlock()
	if !ok {
		return nil, contract.InputInvalid("请重新选择教材")
	}
	url, err := service.files.URL(item.path)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	return url, nil
}

func (service *Service) OpenPath(id string) (string, error) {
	service.mu.Lock()
	item, ok := service.books[id]
	service.mu.Unlock()
	if !ok {
		return "", contract.InputInvalid("请重新选择教材")
	}
	return item.path, nil
}

func (service *Service) Manual(caller Caller, id string, raw json.RawMessage) (any, error) {
	var row Highlight
	if len(raw) == 0 || json.Unmarshal(raw, &row) != nil || !validHighlight(row) {
		return nil, contract.InputInvalid("划线数据无效")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	item, ok := service.books[id]
	if !ok {
		return nil, contract.InputInvalid("请重新选择教材")
	}
	if _, err := service.commitLocked(caller, id, item, item.hash, []Highlight{row}, false); err != nil {
		return nil, err
	}
	return map[string]any{"path": item.path, "count": 1}, nil
}

func (service *Service) Clear(caller Caller, id string) (any, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	item, ok := service.books[id]
	if !ok {
		return nil, contract.InputInvalid("请重新选择教材")
	}
	result, err := service.commitLocked(caller, id, item, item.hash, nil, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": item.path, "count": result.Count}, nil
}

func (service *Service) Apply(caller Caller, draftID string, highlightIDs []string) (any, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	current, ok := service.drafts[draftID]
	if !ok {
		return nil, contract.InputInvalid("请重新分析教材")
	}
	if len(highlightIDs) == 0 {
		return nil, contract.InputInvalid("请选择有效的划线")
	}
	seen := map[string]bool{}
	chosen := []Highlight{}
	for _, id := range highlightIDs {
		if seen[id] {
			return nil, contract.InputInvalid("请选择有效的划线")
		}
		seen[id] = true
		found := false
		for _, row := range current.rows {
			if row.ID == id {
				chosen = append(chosen, row)
				found = true
				break
			}
		}
		if !found {
			return nil, contract.InputInvalid("请选择有效的划线")
		}
	}
	item, ok := service.books[current.bookID]
	if !ok {
		return nil, contract.InputInvalid("请重新选择教材")
	}
	if _, err := service.commitLocked(caller, current.bookID, item, current.hash, chosen, false); err != nil {
		return nil, err
	}
	delete(service.drafts, draftID)
	return map[string]any{"path": item.path, "count": len(chosen)}, nil
}

type writeResult struct {
	Pages int `json:"pages"`
	Count int `json:"count"`
}

func (service *Service) commitLocked(
	caller Caller,
	bookID string,
	item book,
	expected string,
	rows []Highlight,
	clear bool,
) (writeResult, error) {
	current, err := fileHash(item.path)
	if err != nil {
		return writeResult{}, contract.Unavailable("教材文件不可读")
	}
	if current != expected {
		return writeResult{}, contract.InputInvalid("教材已被其他程序修改，请重新选择教材后分析")
	}
	info, err := inspect(caller, item.path)
	if err != nil {
		return writeResult{}, err
	}
	resolved := []Highlight{}
	if !clear {
		merged := append(append([]Highlight{}, rows...), info.Previous...)
		resolved = prioritizedHighlights(merged)
	}
	payload := make([]map[string]any, 0, len(resolved))
	for _, row := range resolved {
		color := highlightColors[row.Source]
		description := highlightLabels[row.Source]
		if row.Extension {
			description += " · 适当延伸"
		}
		description += "\n" + row.Quote + "\n依据：" + row.Evidence
		payload = append(payload, map[string]any{
			"id": row.ID, "page": row.Page, "rect": row.Rect, "source": row.Source,
			"quote": row.Quote, "evidence": row.Evidence, "extension": row.Extension,
			"color": []float64{color[0], color[1], color[2]}, "description": description,
		})
	}
	temp := item.path + ".zynote-" + ids.New() + ".tmp"
	defer os.Remove(temp)
	raw, err := caller.Call("pdf.write", map[string]any{
		"path": item.path, "output": temp, "rows": payload,
	}, 120000)
	if err != nil {
		return writeResult{}, contract.Unavailable(cleanCallerError(err))
	}
	var result writeResult
	if err := json.Unmarshal(raw, &result); err != nil || result.Pages != info.Pages {
		return writeResult{}, contract.InputInvalid("保存检查失败，原教材未修改")
	}
	after, err := fileHash(item.path)
	if err != nil || after != expected {
		return writeResult{}, contract.InputInvalid("教材在保存前发生变化，未覆盖原文件")
	}
	if err := copyExclusive(item.path, item.path+".划线前备份.pdf"); err != nil {
		return writeResult{}, contract.Unavailable("教材备份失败，原教材未修改")
	}
	if err := os.Rename(temp, item.path); err != nil {
		return writeResult{}, contract.Unavailable("教材保存失败，原教材未修改")
	}
	updated, err := fileHash(item.path)
	if err != nil {
		return writeResult{}, contract.Unavailable("教材保存后校验失败")
	}
	item.hash = updated
	service.books[bookID] = item
	return result, nil
}

func copyExclusive(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}

func (service *Service) AddDraft(bookID, hash string, rows []Highlight) string {
	service.mu.Lock()
	defer service.mu.Unlock()
	id := ids.New()
	service.drafts[id] = draft{bookID: bookID, hash: hash, rows: rows}
	return id
}
