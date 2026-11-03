package subtitlederived

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

const publicationLimit = 32 * 1024 * 1024
const receiptLimit = 512 * 1024

type PublicationRef struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}
type publicationFile struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"` // library relative, never an arbitrary path
	SHA256     string `json:"sha256"`
	ByteLength int    `json:"byteLength"`
}
type publicationSpan struct {
	State      string `json:"state"` // present or not-present (outputVersion0)
	ByteOffset int    `json:"byteOffset,omitempty"`
	ByteLength int    `json:"byteLength,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}
type publicationPoint struct {
	Point      Point           `json:"point"` // original pre-anchor verified row
	ArrayIndex int             `json:"arrayIndex"`
	MD         publicationSpan `json:"md"`
}
type publicationPlan struct {
	Version            int                      `json:"version"`
	TaskID             string                   `json:"taskID"`
	ItemIndex          int                      `json:"itemIndex"`
	OutputVersion      int                      `json:"outputVersion"`
	Evidence           contract.SummaryEvidence `json:"evidence"`
	Files              []publicationFile        `json:"files"`
	Points             []publicationPoint       `json:"points"`
	MarkdownByteLength int                      `json:"markdownByteLength"`
	MarkdownSHA256     string                   `json:"markdownSHA256"`
	MDPositionScope    string                   `json:"mdPositionScope"`
}
type publicationReceipt struct {
	Version         int                      `json:"version"`
	TaskID          string                   `json:"taskID"`
	ItemIndex       int                      `json:"itemIndex"`
	PlanSHA256      string                   `json:"planSHA256"`
	Evidence        contract.SummaryEvidence `json:"evidence"`
	Files           []publicationFile        `json:"files"`
	PointCount      int                      `json:"pointCount"`
	MDPositionScope string                   `json:"mdPositionScope"`
}

func publicationError() error {
	return contract.ObservationFailure(fmt.Errorf("笔记发布凭据无效或保存失败；已公开文件保留，未自动重试模型"))
}
func (service *Service) publicationStep(ctx context.Context, step string) error {
	if ctx.Err() != nil {
		return publicationError()
	}
	if service.publicationHook != nil && service.publicationHook(step) != nil {
		return publicationError()
	}
	return nil
}

type publicationReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader publicationReader) Read(body []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(body)
}

// The stream limit, rather than reported stat size, bounds allocation and reads.
func readPublicationBytes(ctx context.Context, path, hash string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, publicationError()
	}
	resolved, err := canonicalEvidencePath(path)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path)) {
		return nil, publicationError()
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, publicationError()
	}
	defer file.Close()
	hasher := sha256.New()
	body, err := io.ReadAll(io.TeeReader(io.LimitReader(publicationReader{ctx, file}, int64(limit)+1), hasher))
	if err != nil || len(body) > limit || hex.EncodeToString(hasher.Sum(nil)) != hash {
		return nil, publicationError()
	}
	return body, nil
}
func decodePublication(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return publicationError()
	}
	return nil
}

// Encode rows independently so a large plan cannot allocate an unbounded
// intermediate JSON document before its publication limit is checked.
func encodePublicationPlan(ctx context.Context, plan publicationPlan) ([]byte, error) {
	rows := plan.Points
	plan.Points = nil
	header, err := json.Marshal(plan)
	if err != nil {
		return nil, publicationError()
	}
	marker := []byte(`"points":null`)
	at := bytes.Index(header, marker)
	if at < 0 {
		return nil, publicationError()
	}
	var output bytes.Buffer
	appendBytes := func(body []byte) error {
		if ctx.Err() != nil || len(body) > publicationLimit-output.Len() {
			return publicationError()
		}
		_, err := output.Write(body)
		return err
	}
	if appendBytes(header[:at]) != nil || appendBytes([]byte(`"points":[`)) != nil {
		return nil, publicationError()
	}
	for i, row := range rows {
		// Six is the maximum JSON escape expansion of one UTF8 input byte.
		if len(row.Point.Quote)+len(row.Point.Point)+len(row.Point.Topic)+len(row.Point.Timestamp) > (publicationLimit-2048)/6 {
			return nil, publicationError()
		}
		body, err := json.Marshal(row)
		if err != nil {
			return nil, publicationError()
		}
		if i > 0 && appendBytes([]byte(",")) != nil {
			return nil, publicationError()
		}
		if appendBytes(body) != nil {
			return nil, publicationError()
		}
	}
	if appendBytes([]byte("]")) != nil || appendBytes(header[at+len(marker):]) != nil {
		return nil, publicationError()
	}
	return output.Bytes(), nil
}
func publicationBlobPath(directory, kind, hash string) (string, error) {
	if !recipeHashPattern.MatchString(hash) || (kind != "blob" && kind != "plan" && kind != "receipt") {
		return "", publicationError()
	}
	return filepath.Join(directory, "publication-"+kind+"-"+hash), nil
}
func publishPublicationBytes(ctx context.Context, directory, kind string, body []byte, limit int) (string, error) {
	if len(body) > limit || ctx.Err() != nil {
		return "", publicationError()
	}
	hash := digest(string(body))
	path, err := publicationBlobPath(directory, kind, hash)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); err == nil {
		_, err = readPublicationBytes(ctx, path, hash, limit)
		return hash, err
	} else if !os.IsNotExist(err) {
		return "", publicationError()
	}
	temporary, err := os.CreateTemp(directory, ".publication-*")
	if err != nil {
		return "", publicationError()
	}
	defer os.Remove(temporary.Name())
	_, writeErr := io.Copy(temporary, publicationReader{ctx, bytes.NewReader(body)})
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return "", publicationError()
	}
	if err := os.Link(temporary.Name(), path); err != nil {
		if _, checkErr := readPublicationBytes(ctx, path, hash, limit); checkErr != nil {
			return "", publicationError()
		}
	}
	_, err = readPublicationBytes(ctx, path, hash, limit)
	return hash, err
}

// Check each controlled directory, including junctions, before touching output.
func (service *Service) publicationPath(relative string) (string, error) {
	return service.locatePublicationPath(relative, false)
}
func (service *Service) locatePublicationPath(relative string, create bool) (string, error) {
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return "", publicationError()
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if (len(parts) != 2 && len(parts) != 3) || parts[0] != "Notes" {
		return "", publicationError()
	}
	for _, part := range parts {
		if part == ".." || part == "." || part == "" {
			return "", publicationError()
		}
	}
	base, err := canonicalEvidencePath(service.Library)
	if err != nil {
		return "", publicationError()
	}
	current := base
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		if create {
			if err := os.Mkdir(current, 0755); err != nil && !os.IsExist(err) {
				return "", publicationError()
			}
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", publicationError()
		}
		resolved, err := canonicalEvidencePath(current)
		if err != nil || !strings.EqualFold(resolved, current) {
			return "", publicationError()
		}
	}
	return filepath.Join(current, parts[len(parts)-1]), nil
}

func publicationRelativePath(task *Task, index int, kind string) string {
	item := &task.Items[index]
	title := []rune(library.FolderName(item.Source.Title))
	if len(title) > 40 {
		title = title[:40]
	}
	stem := string(title) + "-" + modeTitles[item.Mode] + "-" + task.ID[:8] + "-" + itoa(index+1)
	directory := "Notes"
	if task.CourseName != "" {
		directory = filepath.Join(directory, library.FolderName(task.CourseName))
	}
	return filepath.Join(directory, stem+"."+kind)
}
func (service *Service) buildPublication(ctx context.Context, task *Task, index int, srt string, deps Dependencies) (publicationPlan, [][]byte, error) {
	item := &task.Items[index]
	plan := publicationPlan{Version: 1, TaskID: task.ID, ItemIndex: index, OutputVersion: task.OutputVersion}
	if item.Prepared == nil || item.Prepared.Evidence == nil || service.verifyPointEvidence(ctx, task, item, item.Prepared.Points, item.Prepared.Evidence) != nil {
		return plan, nil, publicationError()
	}
	plan.Evidence = *item.Prepared.Evidence
	plan.MarkdownByteLength = len(item.Prepared.Markdown)
	plan.MarkdownSHA256 = digest(item.Prepared.Markdown)
	plan.MDPositionScope = "none"
	if task.OutputVersion == 2 {
		plan.MDPositionScope = "anchor-appendix"
	}
	points := append([]Point(nil), item.Prepared.Points...)
	anchorText := ""
	if task.OutputVersion == 2 {
		if ctx.Err() != nil {
			return plan, nil, publicationError()
		}
		var err error
		points, anchorText, err = anchorPoints(srt, points)
		if err != nil || ctx.Err() != nil {
			return plan, nil, publicationError()
		}
	} else if task.OutputVersion != 0 {
		return plan, nil, publicationError()
	}
	for i, point := range item.Prepared.Points {
		if ctx.Err() != nil {
			return plan, nil, publicationError()
		}
		point.Anchor = nil
		row := publicationPoint{Point: point, ArrayIndex: i, MD: publicationSpan{State: "not-present"}}
		plan.Points = append(plan.Points, row)
	}
	// anchorPoints uses a fixed four-line prefix and one line per original row.
	// Sequential construction counts UTF8 bytes, never searches duplicate text.
	if task.OutputVersion == 2 {
		lines := strings.Split(anchorText, "\n")
		offset := len(item.Prepared.Markdown)
		pointIndex := 0
		for i, line := range lines {
			if ctx.Err() != nil {
				return plan, nil, publicationError()
			}
			if i >= 6 { // two initial empty lines, heading, blank, explanation, blank
				if pointIndex >= len(plan.Points) {
					return plan, nil, publicationError()
				}
				plan.Points[pointIndex].MD = publicationSpan{State: "present", ByteOffset: offset, ByteLength: len(line), SHA256: digest(line)}
				pointIndex++
			}
			offset += len(line) + 1
		}
		if pointIndex != len(plan.Points) {
			return plan, nil, publicationError()
		}
	}
	label := item.Source.URL
	if item.Source.Kind == "direct-srt" {
		label = "传入 SRT 快照：" + item.Source.Path
	}
	provenance := "\n\n---\n字幕来源：" + label + "\n来源版本：" + item.Source.Generation + "\n字幕 SHA256：" + item.Source.Hash + "\n"
	contents := [][]byte{[]byte(item.Prepared.Markdown + anchorText + provenance), []byte(prettyJSON(points))}
	kinds := []string{"md", "json"}
	if item.Mode == "mindmap" && deps.RenderSVG != nil {
		contents = append(contents, []byte(deps.RenderSVG(item.Source.Title, points)))
		kinds = append(kinds, "svg")
	}
	for i, body := range contents {
		if len(body) > publicationLimit {
			return plan, nil, publicationError()
		}
		relative := publicationRelativePath(task, index, kinds[i])
		if _, err := service.locatePublicationPath(relative, true); err != nil {
			return plan, nil, err
		}
		plan.Files = append(plan.Files, publicationFile{Kind: kinds[i], Path: relative, SHA256: digest(string(body)), ByteLength: len(body)})
	}
	return plan, contents, nil
}
func (service *Service) loadPublication(ctx context.Context, task *Task, index int) (publicationPlan, string, error) {
	return service.loadPublicationAudit(ctx, task, index, nil)
}
func (service *Service) loadPublicationAudit(ctx context.Context, task *Task, index int, checked *AttemptAudit) (publicationPlan, string, error) {
	plan := publicationPlan{}
	item := &task.Items[index]
	ref := item.PublicationPlan
	if ref == nil || ref.Version != 1 {
		return plan, "", publicationError()
	}
	directory, err := locateEvidenceDirectory(service.Root, task.ID, index, false)
	if err != nil {
		return plan, "", publicationError()
	}
	path, err := publicationBlobPath(directory, "plan", ref.SHA256)
	if err != nil {
		return plan, "", err
	}
	body, err := readPublicationBytes(ctx, path, ref.SHA256, publicationLimit)
	if err != nil || decodePublication(body, &plan) != nil {
		return plan, "", publicationError()
	}
	if plan.Version != 1 || plan.TaskID != task.ID || plan.ItemIndex != index || plan.OutputVersion != task.OutputVersion || item.SummaryEvidence == nil || plan.Evidence != *item.SummaryEvidence || len(plan.Files) < 2 || len(plan.Files) > 3 || len(plan.Points) > 280000 || len(plan.Points) != plan.Evidence.PointCount {
		return plan, "", publicationError()
	}
	if (task.OutputVersion == 0 && plan.MDPositionScope != "none") || (task.OutputVersion == 2 && plan.MDPositionScope != "anchor-appendix") {
		return plan, "", publicationError()
	}
	points := make([]Point, len(plan.Points))
	for i, row := range plan.Points {
		if ctx.Err() != nil {
			return plan, "", publicationError()
		}
		if row.ArrayIndex != i || row.Point.Anchor != nil {
			return plan, "", publicationError()
		}
		points[i] = row.Point
	}
	if service.verifyPointEvidenceAudit(ctx, task, item, points, &plan.Evidence, checked) != nil {
		return plan, "", publicationError()
	}
	for i, file := range plan.Files {
		if file.Kind != []string{"md", "json", "svg"}[i] || file.ByteLength < 0 || file.ByteLength > publicationLimit || !recipeHashPattern.MatchString(file.SHA256) || file.Path != publicationRelativePath(task, index, file.Kind) || (file.Kind == "svg" && item.Mode != "mindmap") {
			return plan, "", publicationError()
		}
		if _, err := service.publicationPath(file.Path); err != nil {
			return plan, "", err
		}
	}
	return plan, directory, nil
}
func validatePublicationRows(ctx context.Context, plan publicationPlan, bodies [][]byte, srt string) error {
	if ctx.Err() != nil {
		return publicationError()
	}
	if len(bodies) != len(plan.Files) {
		return publicationError()
	}
	if plan.MarkdownByteLength < 0 || plan.MarkdownByteLength > len(bodies[0]) || digest(string(bodies[0][:plan.MarkdownByteLength])) != plan.MarkdownSHA256 {
		return publicationError()
	}
	points := make([]Point, len(plan.Points))
	for i, row := range plan.Points {
		if ctx.Err() != nil {
			return publicationError()
		}
		points[i] = row.Point
	}
	var anchorText string
	if plan.OutputVersion == 2 {
		if ctx.Err() != nil {
			return publicationError()
		}
		var err error
		points, anchorText, err = anchorPoints(srt, points)
		if err != nil {
			return publicationError()
		}
		if ctx.Err() != nil {
			return publicationError()
		}
	}
	var actual []Point
	if decodePublication(bodies[1], &actual) != nil || !reflect.DeepEqual(actual, points) {
		return publicationError()
	}
	lines := strings.Split(anchorText, "\n")
	lastEnd := -1
	expectedOffset := plan.MarkdownByteLength
	if plan.OutputVersion == 2 {
		if !bytes.HasPrefix(bodies[0][plan.MarkdownByteLength:], []byte(anchorText)) {
			return publicationError()
		}
		for _, line := range lines[:6] {
			expectedOffset += len(line) + 1
		}
	}
	for i, row := range plan.Points {
		if ctx.Err() != nil {
			return publicationError()
		}
		span := row.MD
		if plan.OutputVersion == 0 {
			if span != (publicationSpan{State: "not-present"}) {
				return publicationError()
			}
			continue
		}
		if span.State != "present" || span.ByteOffset != expectedOffset || span.ByteLength < 0 || span.ByteOffset > len(bodies[0])-span.ByteLength || span.ByteOffset <= lastEnd || len(lines) <= i+6 {
			return publicationError()
		}
		segment := bodies[0][span.ByteOffset : span.ByteOffset+span.ByteLength]
		if digest(string(segment)) != span.SHA256 || string(segment) != lines[i+6] {
			return publicationError()
		}
		lastEnd = span.ByteOffset + span.ByteLength
		expectedOffset = lastEnd + 1
	}
	return nil
}
func (service *Service) publicationFiles(ctx context.Context, plan publicationPlan, directory string, published bool) ([][]byte, error) {
	bodies := make([][]byte, 0, len(plan.Files))
	for _, file := range plan.Files {
		path, err := publicationBlobPath(directory, "blob", file.SHA256)
		if published {
			path, err = service.publicationPath(file.Path)
		}
		if err != nil {
			return nil, err
		}
		body, err := readPublicationBytes(ctx, path, file.SHA256, publicationLimit)
		if err != nil || len(body) != file.ByteLength {
			return nil, publicationError()
		}
		bodies = append(bodies, body)
	}
	return bodies, nil
}
func (service *Service) publishSummary(ctx context.Context, task *Task, index int, srt string, deps Dependencies) error {
	item := &task.Items[index]
	if item.PublicationReceipt != nil {
		return publicationError()
	} // committed receipt is never recreated
	if item.PublicationPlan == nil {
		plan, bodies, err := service.buildPublication(ctx, task, index, srt, deps)
		if err != nil {
			return err
		}
		directory, err := evidenceDirectory(service.Root, task.ID, index)
		if err != nil {
			return publicationError()
		}
		for _, body := range bodies {
			if err := service.publicationStep(ctx, "blob-before"); err != nil {
				return err
			}
			if _, err := service.publishTaskBytes(ctx, *task, directory, "blob", body, publicationLimit); err != nil {
				return err
			}
		}
		body, err := encodePublicationPlan(ctx, plan)
		if err != nil {
			return publicationError()
		}
		if err := service.publicationStep(ctx, "plan-before"); err != nil {
			return err
		}
		hash, err := service.publishTaskBytes(ctx, *task, directory, "plan", body, publicationLimit)
		if err != nil {
			return err
		}
		if err := service.publicationStep(ctx, "plan-save-before"); err != nil {
			return err
		}
		candidate := *task
		candidate.Items = append([]Item(nil), task.Items...)
		ref := &PublicationRef{Version: 1, SHA256: hash}
		candidate.Items[index].PublicationPlan = ref
		if service.save(candidate) != nil {
			return publicationError()
		}
		item.PublicationPlan = ref
		if err := service.publicationStep(ctx, "plan-durable"); err != nil {
			return err
		}
	}
	plan, directory, err := service.loadPublication(ctx, task, index)
	if err != nil {
		return err
	}
	bodies, err := service.publicationFiles(ctx, plan, directory, false)
	if err != nil {
		return err
	}
	if validatePublicationRows(ctx, plan, bodies, srt) != nil {
		return publicationError()
	}
	for i, file := range plan.Files {
		if err := service.publicationStep(ctx, "file-before-"+file.Kind); err != nil {
			return err
		}
		current, err := service.current(item.Source, deps, ctx)
		if err != nil || digest(current) != plan.Evidence.SourceSHA256 {
			return publicationError()
		}
		path, err := service.publicationPath(file.Path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			temporary, err := os.CreateTemp(filepath.Dir(path), ".note-*")
			if err != nil {
				return publicationError()
			}
			_, writeErr := io.Copy(temporary, publicationReader{ctx, bytes.NewReader(bodies[i])})
			syncErr := temporary.Sync()
			closeErr := temporary.Close()
			if writeErr == nil && syncErr == nil && closeErr == nil && ctx.Err() == nil {
				writeErr = service.withTaskFence(ctx, *task, func() error { return os.Link(temporary.Name(), path) })
			}
			_ = os.Remove(temporary.Name())
			if writeErr != nil || syncErr != nil || closeErr != nil || ctx.Err() != nil {
				return publicationError()
			}
		}
		// Public files deliberately never enter Run's created cleanup list.
		if _, err := readPublicationBytes(ctx, path, file.SHA256, publicationLimit); err != nil {
			return err
		}
		if err := service.publicationStep(ctx, "file-durable-"+file.Kind); err != nil {
			return err
		}
	}
	current, err := service.current(item.Source, deps, ctx)
	if err != nil || digest(current) != plan.Evidence.SourceSHA256 {
		return publicationError()
	}
	bodies, err = service.publicationFiles(ctx, plan, directory, true)
	if err != nil || validatePublicationRows(ctx, plan, bodies, current) != nil {
		return publicationError()
	}
	receipt := publicationReceipt{Version: 1, TaskID: task.ID, ItemIndex: index, PlanSHA256: item.PublicationPlan.SHA256, Evidence: plan.Evidence, Files: plan.Files, PointCount: len(plan.Points), MDPositionScope: plan.MDPositionScope}
	body, err := json.Marshal(receipt)
	if err != nil {
		return publicationError()
	}
	if err := service.publicationStep(ctx, "receipt-before"); err != nil {
		return err
	}
	hash, err := service.publishTaskBytes(ctx, *task, directory, "receipt", body, receiptLimit)
	if err != nil {
		return err
	}
	if err := service.publicationStep(ctx, "receipt-durable"); err != nil {
		return err
	}
	candidate := *task
	candidate.Items = append([]Item(nil), task.Items...)
	completed := candidate.Items[index]
	completed.PublicationReceipt = &PublicationRef{Version: 1, SHA256: hash}
	completed.Outputs = nil
	for _, file := range plan.Files {
		path, err := service.publicationPath(file.Path)
		if err != nil {
			return err
		}
		completed.Outputs = append(completed.Outputs, Output{Path: path, Hash: file.SHA256})
	}
	completed.Prepared = nil
	completed.Status = "已完成"
	completed.Error = ""
	candidate.Items[index] = completed
	if err := service.publicationStep(ctx, "commit-before"); err != nil {
		return err
	}
	if service.save(candidate) != nil {
		return publicationError()
	}
	*item = completed
	return service.publicationStep(ctx, "commit-durable")
}

func (service *Service) publishTaskBytes(ctx context.Context, task Task, directory, kind string, body []byte, limit int) (string, error) {
	var hash string
	err := service.withTaskFence(ctx, task, func() error {
		var err error
		hash, err = publishPublicationBytes(ctx, directory, kind, body, limit)
		return err
	})
	return hash, err
}
func (service *Service) restorePublication(ctx context.Context, task *Task, index int, srt string) error {
	if err := service.verifyPublication(ctx, task, index, srt, nil); err != nil {
		return err
	}
	item := &task.Items[index]
	item.Status = "已完成"
	item.Error = ""
	return nil
}
func (service *Service) verifyPublication(ctx context.Context, task *Task, index int, srt string, checked *AttemptAudit) error {
	item := &task.Items[index]
	ref := item.PublicationReceipt
	if ref == nil || ref.Version != 1 {
		return publicationError()
	}
	plan, directory, err := service.loadPublicationAudit(ctx, task, index, checked)
	if err != nil {
		return err
	}
	path, err := publicationBlobPath(directory, "receipt", ref.SHA256)
	if err != nil {
		return err
	}
	body, err := readPublicationBytes(ctx, path, ref.SHA256, receiptLimit)
	if err != nil {
		return err
	}
	var receipt publicationReceipt
	if decodePublication(body, &receipt) != nil || receipt.Version != 1 || receipt.TaskID != task.ID || receipt.ItemIndex != index || receipt.PlanSHA256 != item.PublicationPlan.SHA256 || receipt.Evidence != plan.Evidence || receipt.MDPositionScope != plan.MDPositionScope || !reflect.DeepEqual(receipt.Files, plan.Files) || receipt.PointCount != len(plan.Points) || len(item.Outputs) != len(plan.Files) {
		return publicationError()
	}
	for i, file := range plan.Files {
		path, err := service.publicationPath(file.Path)
		if err != nil || item.Outputs[i] != (Output{Path: path, Hash: file.SHA256}) {
			return publicationError()
		}
	}
	bodies, err := service.publicationFiles(ctx, plan, directory, true)
	if err != nil || validatePublicationRows(ctx, plan, bodies, srt) != nil {
		return publicationError()
	}
	return nil
}
