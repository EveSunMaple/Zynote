package textbook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

const (
	msgReselectTextbook = "请重新选择教材"
	msgHighlightInvalid = "划线数据无效"
	msgInvalidHighlight = "请选择有效的划线"
)

type Caller interface {
	Call(method string, params any, timeoutMS int) (json.RawMessage, error)
}

type book struct {
	annotations []Highlight
	path        string
	hash        string
	pages       int
	info        os.FileInfo
}

func (item book) selection(id string) map[string]any {
	return map[string]any{"id": id, "path": item.path, "pages": item.pages}
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

func inspectBook(caller Caller, path string) (book, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return book{}, contract.InputInvalid("教材路径无效")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() || strings.ToLower(filepath.Ext(absolute)) != ".pdf" {
		return book{}, contract.InputInvalid("找不到有效的教材 PDF，请重新选择文件")
	}
	parsed, err := inspect(caller, absolute)
	if err != nil {
		return book{}, err
	}
	hash, err := fileHash(absolute)
	if err != nil {
		return book{}, contract.Unavailable("教材文件不可读")
	}
	return book{path: absolute, hash: hash, pages: parsed.Pages, info: info, annotations: parsed.Previous}, nil
}

func (service *Service) Select(caller Caller, path string, register func(string) (string, error)) (any, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	item, err := inspectBook(caller, path)
	if err != nil {
		return nil, err
	}
	id, err := register(item.path)
	if err != nil {
		return nil, err
	}
	service.books[id] = item
	return item.selection(id), nil
}

// Resolve accepts the trusted registry path, never a path supplied by the renderer.
func (service *Service) Resolve(caller Caller, id, path string) (any, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if current, ok := service.books[id]; ok && current.path == path {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, contract.InputInvalid("找不到有效的教材 PDF，请重新选择文件")
		}
		if os.SameFile(info, current.info) && info.Size() == current.info.Size() && info.ModTime().Equal(current.info.ModTime()) {
			return current.selection(id), nil
		}
	}
	item, err := inspectBook(caller, path)
	if err != nil {
		return nil, err
	}
	service.books[id] = item
	return item.selection(id), nil
}

func (service *Service) Bytes(id string) (any, error) {
	service.mu.Lock()
	item, ok := service.books[id]
	service.mu.Unlock()
	if !ok {
		return nil, contract.InputInvalid(msgReselectTextbook)
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
		return "", contract.InputInvalid(msgReselectTextbook)
	}
	return item.path, nil
}

func parseManualHighlights(raw json.RawMessage) ([]Highlight, error) {
	var rows []Highlight
	if len(raw) > 0 && raw[0] == '{' {
		var single Highlight
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil, contract.InputInvalid(msgHighlightInvalid)
		}
		rows = []Highlight{single}
	} else if json.Unmarshal(raw, &rows) != nil {
		return nil, contract.InputInvalid(msgHighlightInvalid)
	}
	if len(rows) == 0 || len(rows) > 512 {
		return nil, contract.InputInvalid("一次请选择当前页的 1 到 512 段文字或笔迹")
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if !validHighlight(row) || seen[row.ID] || row.Page != rows[0].Page {
			return nil, contract.InputInvalid(msgHighlightInvalid)
		}
		seen[row.ID] = true
	}
	return rows, nil
}

func validateManualRows(rows []Highlight, pages int) error {
	for _, row := range rows {
		if row.Chapter != nil && row.Chapter.LastPage > pages {
			return contract.InputInvalid("章节范围超出教材页数")
		}
		for _, comment := range row.Comments {
			if comment.Kind != "personal" {
				return contract.InputInvalid("手动批注只能标记为我的笔记")
			}
		}
	}
	return nil
}

func (service *Service) Manual(caller Caller, id string, raw json.RawMessage) (any, error) {
	rows, err := parseManualHighlights(raw)
	if err != nil {
		return nil, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	item, ok := service.books[id]
	if !ok {
		return nil, contract.InputInvalid(msgReselectTextbook)
	}
	if err := validateManualRows(rows, item.pages); err != nil {
		return nil, err
	}
	if _, err := service.commitLocked(caller, id, item, item.hash, addAnnotations(rows)); err != nil {
		return nil, err
	}
	return map[string]any{"path": item.path, "count": len(rows)}, nil
}

func (service *Service) Clear(caller Caller, id string) (any, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	item, ok := service.books[id]
	if !ok {
		return nil, contract.InputInvalid(msgReselectTextbook)
	}
	result, err := service.commitLocked(caller, id, item, item.hash, func(_ []Highlight) ([]Highlight, error) { return []Highlight{}, nil })
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
		return nil, contract.InputInvalid(msgInvalidHighlight)
	}
	seen := map[string]bool{}
	chosen := []Highlight{}
	for _, id := range highlightIDs {
		if seen[id] {
			return nil, contract.InputInvalid(msgInvalidHighlight)
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
			return nil, contract.InputInvalid(msgInvalidHighlight)
		}
	}
	item, ok := service.books[current.bookID]
	if !ok {
		return nil, contract.InputInvalid(msgReselectTextbook)
	}
	if _, err := service.commitLocked(caller, current.bookID, item, current.hash, addAnnotations(chosen)); err != nil {
		return nil, err
	}
	delete(service.drafts, draftID)
	return map[string]any{"path": item.path, "count": len(chosen)}, nil
}

type writeResult struct {
	Pages int `json:"pages"`
	Count int `json:"count"`
}

func uniqueHighlightIDs(rows []Highlight) error {
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.ID] {
			return contract.InputInvalid("批注标识重复，请重新选择划线")
		}
		seen[row.ID] = true
	}
	return nil
}

func annotationDescription(row Highlight) string {
	description := highlightLabels[row.Source]
	if row.Extension {
		description += " · 适当延伸"
	}
	description += "\n" + row.Quote + "\n依据：" + row.Evidence
	if row.Chapter != nil {
		description = row.Chapter.Text
		for _, ref := range row.Chapter.Refs {
			description += fmt.Sprintf("\n第%d页 第%d行", ref.Page, ref.Line)
		}
		return description
	}
	if len(row.Comments) == 0 {
		return description
	}
	var lines []string
	for _, comment := range row.Comments {
		label := map[string]string{"teacher": "老师讲解", "ai": highlightLabels["ai"], "personal": "我的笔记"}[comment.Kind]
		lines = append(lines, label+" · "+comment.Text)
		if comment.Quote != "" {
			lines = append(lines, strings.TrimSpace(comment.Timestamp+" "+comment.Quote))
		}
	}
	return strings.Join(lines, "\n")
}

func annotationPayload(rows []Highlight) []map[string]any {
	payload := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		color := highlightColors[row.Source]
		annotation := map[string]any{
			"id": row.ID, "page": row.Page, "rect": row.Rect, "source": row.Source,
			"quote": row.Quote, "evidence": row.Evidence, "extension": row.Extension,
			"color": []float64{color[0], color[1], color[2]}, "description": annotationDescription(row),
		}
		if len(row.Comments) > 0 {
			annotation["comments"] = row.Comments
		}
		if row.Chapter != nil {
			annotation["chapter"] = row.Chapter
		}
		if len(row.Ink) > 0 {
			annotation["ink"] = row.Ink
		}
		payload = append(payload, annotation)
	}
	return payload
}

func (service *Service) commitLocked(
	caller Caller,
	bookID string,
	item book,
	expected string,
	change func([]Highlight) ([]Highlight, error),
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
	resolved, err := change(slices.Clone(info.Previous))
	if err != nil {
		return writeResult{}, err
	}
	// A retried append or an unchanged personal note does not need a new PDF revision.
	if reflect.DeepEqual(info.Previous, resolved) {
		item.annotations = resolved
		service.books[bookID] = item
		return writeResult{Pages: info.Pages, Count: len(resolved)}, nil
	}
	if err := uniqueHighlightIDs(resolved); err != nil {
		return writeResult{}, err
	}
	payload := annotationPayload(resolved)
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
	item.info, _ = os.Stat(item.path)
	item.annotations = resolved
	service.books[bookID] = item
	return result, nil
}

func addAnnotations(rows []Highlight) func([]Highlight) ([]Highlight, error) {
	return func(existing []Highlight) ([]Highlight, error) {
		merged := mergeHighlights(existing, rows)
		for _, r := range merged {
			if len(r.Comments) > 100 {
				return nil, contract.InputInvalid("单条划线最多保留 100 条批注")
			}
		}
		return merged, nil
	}
}

// Reading a page uses the annotations already decoded when selecting or writing the PDF.
func (service *Service) Annotations(id string, page int) ([]Highlight, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	item, ok := service.books[id]
	if !ok {
		return nil, contract.InputInvalid(msgReselectTextbook)
	}
	if page < 1 || page > item.pages {
		return nil, contract.InputInvalid("页码超出教材范围")
	}
	rows := []Highlight{}
	for _, h := range item.annotations {
		if h.Page == page {
			rows = append(rows, h)
		}
	}
	return rows, nil
}

func addPersonalComment(rows []Highlight, markID, text string, previous *string) ([]Highlight, error) {
	for i, row := range rows {
		if row.ID != markID || row.Chapter != nil {
			continue
		}
		comments := slices.Clone(row.Comments)
		next := Comment{Kind: "personal", Text: text}
		if previous != nil {
			old := slices.Index(comments, Comment{Kind: "personal", Text: *previous})
			if old < 0 {
				return nil, contract.InputInvalid("原批注已更改，请重新打开后再编辑")
			}
			comments = slices.Delete(comments, old, old+1)
		}
		if !slices.Contains(comments, next) {
			comments = append(comments, next)
		}
		if len(comments) > 100 {
			return nil, contract.InputInvalid("单条划线最多保留 100 条批注")
		}
		rows[i].Comments = comments
		return rows, nil
	}
	return nil, contract.InputInvalid("这条划线已不存在，请重新选择")
}

func (service *Service) Comment(caller Caller, id, markID, text string, previous *string) (any, error) {
	text = strings.TrimSpace(text)
	if !boundedText(text, 5000) || (previous != nil && !boundedText(*previous, 5000)) {
		return nil, contract.InputInvalid("请填写 1 到 5000 字的批注")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	item, ok := service.books[id]
	if !ok {
		return nil, contract.InputInvalid(msgReselectTextbook)
	}
	return service.commitLocked(caller, id, item, item.hash, func(rows []Highlight) ([]Highlight, error) {
		return addPersonalComment(rows, markID, text, previous)
	})
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

func (service *Service) AddDraft(bookID, hash string, rows []Highlight) (string, []Highlight) {
	service.mu.Lock()
	defer service.mu.Unlock()
	id := ids.New()
	// Deduplicate identical anchors before exposing the IDs accepted by Apply.
	rows = prioritizedHighlights(rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Page < rows[j].Page })
	service.drafts[id] = draft{bookID: bookID, hash: hash, rows: rows}
	return id, rows
}

func (service *Service) Snapshot(id string) (string, string, int, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	item, ok := service.books[id]
	if !ok {
		return "", "", 0, contract.InputInvalid(msgReselectTextbook)
	}
	return item.path, item.hash, item.pages, nil
}
