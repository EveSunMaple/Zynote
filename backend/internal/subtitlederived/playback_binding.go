package subtitlederived

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

// Completed journals contain generated text as well as metadata. Only this new
// bound observer has a 64MiB read ceiling; legacy text recovery retains Read.
const boundJournalMaximumBytes = int64(64 * 1024 * 1024)

func bindingSafeRegular(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("播放绑定记录不可用")
	}
	resolved, err := filepath.EvalSymlinks(path)
	equal := filepath.Clean(resolved) == filepath.Clean(path)
	if runtime.GOOS == "windows" {
		equal = strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path))
	}
	if err != nil || !equal {
		return nil, fmt.Errorf("播放绑定记录路径无效")
	}
	return info, nil
}

// ReadBoundAuthority observes the original immutable creation authority. It
// checks that the journal remains safely readable, but does not read or attest
// its mutable generated text. New opens still use the full ReadBoundTask.
func (service *Service) ReadBoundAuthority(ctx context.Context, id, bindingSHA, authoritySHA string) (Task, error) {
	if !validUUID(id) || !recipeHashPattern.MatchString(bindingSHA) || !recipeHashPattern.MatchString(authoritySHA) {
		return Task{}, fmt.Errorf("播放绑定引用无效")
	}
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	journal := filepath.Join(service.Root, id+".json")
	before, err := bindingSafeRegular(journal)
	if err != nil {
		return Task{}, err
	}
	file, err := os.Open(journal)
	if err != nil {
		return Task{}, err
	}
	opened, statErr := file.Stat()
	closeErr := file.Close()
	after, afterErr := bindingSafeRegular(journal)
	if statErr != nil || closeErr != nil || afterErr != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return Task{}, fmt.Errorf("播放绑定任务不可读")
	}
	path := filepath.Join(service.Root, id+".playback-binding.json")
	if _, err := bindingSafeRegular(path); err != nil {
		return Task{}, err
	}
	body, err := readBindingAuthority(path)
	if err != nil {
		return Task{}, err
	}
	if digest(string(body)) != authoritySHA {
		return Task{}, fmt.Errorf("播放绑定权威记录已改变")
	}
	var authority bindingAuthority
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&authority) != nil || decoder.Decode(&struct{}{}) != io.EOF || authority.Version != 1 || authority.TaskID != id {
		return Task{}, fmt.Errorf("播放绑定权威身份无效")
	}
	binding := authority.Binding
	task := Task{ID: id, CourseName: authority.Course, RequestHash: authority.RequestHash, Items: []Item{{Source: authority.Source, Mode: authority.Mode, SourceBinding: &binding, SourceBindingSHA256: bindingSHA, BindingAuthoritySHA256: authoritySHA}}}
	if err := service.validateTaskBinding(task); err != nil {
		return Task{}, err
	}
	if _, err := service.validateSources([]Source{authority.Source}); err != nil || !validMode(authority.Mode) {
		return Task{}, fmt.Errorf("播放绑定来源无效")
	}
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	return task, nil
}

// ExistingRetainedOperation checks immutable creation identity, not current
// media availability. Run still live-checks any as-yet undispatched provider.
func (service *Service) ExistingRetainedOperation(ctx context.Context, id, mode, course, videoID, receiptSHA string) (Task, bool, error) {
	if !validUUID(id) {
		return Task{}, false, fmt.Errorf("笔记任务标识无效")
	}
	if _, err := os.Lstat(filepath.Join(service.Root, id+".json")); os.IsNotExist(err) {
		return Task{}, false, nil
	} else if err != nil {
		return Task{}, true, err
	}
	task, err := service.ReadBoundTask(ctx, id)
	if err != nil {
		return Task{}, true, err
	}
	item := task.Items[0]
	if item.Mode != mode || task.CourseName != course || item.SourceBinding.VideoTaskID != videoID || item.SourceBinding.RetentionReceiptSHA256 != receiptSHA {
		return Task{}, true, fmt.Errorf("同一提交标识不能更换字幕、课程或笔记形式；原任务保留")
	}
	return task, true, nil
}

func (service *Service) ReadBoundTask(ctx context.Context, id string) (Task, error) {
	if !validUUID(id) {
		return Task{}, fmt.Errorf("笔记任务标识无效")
	}
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	path := filepath.Join(service.Root, id+".json")
	before, err := bindingSafeRegular(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > boundJournalMaximumBytes {
		return Task{}, fmt.Errorf("播放绑定任务过大或不可用；文字成果保留")
	}
	file, err := os.Open(path)
	if err != nil {
		return Task{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return Task{}, fmt.Errorf("播放绑定任务身份不一致")
	}
	var body bytes.Buffer
	chunk := make([]byte, 65536)
	reader := io.LimitReader(file, boundJournalMaximumBytes+1)
	for {
		if err := ctx.Err(); err != nil {
			return Task{}, err
		}
		n, readErr := reader.Read(chunk)
		body.Write(chunk[:n])
		if int64(body.Len()) > boundJournalMaximumBytes {
			return Task{}, fmt.Errorf("播放绑定任务过大；文字成果保留")
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Task{}, readErr
		}
	}
	after, err := bindingSafeRegular(path)
	if err != nil || int64(body.Len()) != opened.Size() || before.Size() != opened.Size() || !before.ModTime().Equal(opened.ModTime()) || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return Task{}, fmt.Errorf("播放绑定任务身份不一致")
	}
	var task Task
	if err := json.Unmarshal(body.Bytes(), &task); err != nil || task.ID != id || len(task.Items) != 1 || task.Items[0].SourceBinding == nil {
		return Task{}, fmt.Errorf("摘要不含已核播放绑定")
	}
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	if err := service.validateTaskBinding(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func bindingRequest(sources []Source, modes []string, course string, binding *contract.PlaybackSourceBinding) ([]byte, error) {
	return json.Marshal(struct {
		Sources       []Source
		Modes         []string
		Course        string
		SourceBinding *contract.PlaybackSourceBinding `json:",omitempty"`
	}{sources, modes, course, binding})
}

type bindingAuthority struct {
	Version     int                            `json:"version"`
	TaskID      string                         `json:"taskID"`
	Course      string                         `json:"course"`
	Source      Source                         `json:"source"`
	Mode        string                         `json:"mode"`
	RequestHash string                         `json:"requestHash"`
	Binding     contract.PlaybackSourceBinding `json:"binding"`
}

func taskBindingAuthority(task Task) ([]byte, error) {
	item := task.Items[0]
	return json.Marshal(bindingAuthority{1, task.ID, task.CourseName, item.Source, item.Mode, task.RequestHash, *item.SourceBinding})
}

func (service *Service) createBindingAuthority(task *Task) error {
	body, err := taskBindingAuthority(*task)
	if err != nil || len(body) > 65536 {
		return fmt.Errorf("播放绑定身份无效")
	}
	path := filepath.Join(service.Root, task.ID+".playback-binding.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		previous, readErr := readBindingAuthority(path)
		if readErr != nil || !bytes.Equal(previous, body) {
			return fmt.Errorf("原播放绑定身份不可更换")
		}
	} else if err != nil {
		return err
	} else {
		_, writeErr := file.Write(body)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return fmt.Errorf("播放绑定保存失败")
		}
	}
	task.Items[0].BindingAuthoritySHA256 = digest(string(body))
	return nil
}

func readBindingAuthority(path string) ([]byte, error) {
	before, err := bindingSafeRegular(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 65536 {
		return nil, fmt.Errorf("播放绑定记录无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("播放绑定记录无效")
	}
	body, err := io.ReadAll(io.LimitReader(file, 65537))
	after, statErr := bindingSafeRegular(path)
	if err != nil || statErr != nil || len(body) > 65536 || int64(len(body)) != opened.Size() || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, fmt.Errorf("播放绑定记录无效")
	}
	return body, nil
}

func (service *Service) validateTaskBinding(task Task) error {
	for _, item := range task.Items {
		if item.SourceBinding == nil {
			if item.SourceBindingSHA256 != "" || item.BindingAuthoritySHA256 != "" {
				return fmt.Errorf("播放绑定身份不一致")
			}
			continue
		}
		if len(task.Items) != 1 || item.Source.Kind != "direct-srt" || !videoppt.ValidPlaybackBinding(*item.SourceBinding) || item.Source.Hash != item.SourceBinding.SubtitleSHA256 || item.SourceBindingSHA256 != videoppt.PlaybackBindingSHA256(*item.SourceBinding) {
			return fmt.Errorf("播放绑定身份不一致")
		}
		request, err := bindingRequest([]Source{item.Source}, []string{item.Mode}, task.CourseName, item.SourceBinding)
		if err != nil || digest(string(request)) != task.RequestHash {
			return fmt.Errorf("播放绑定请求身份不一致")
		}
		expected, err := taskBindingAuthority(task)
		actual, readErr := readBindingAuthority(filepath.Join(service.Root, task.ID+".playback-binding.json"))
		if err != nil || readErr != nil || !bytes.Equal(expected, actual) || digest(string(actual)) != item.BindingAuthoritySHA256 {
			return fmt.Errorf("播放绑定记录身份不一致")
		}
	}
	// Removing binding fields from a bound journal must not silently downgrade it.
	if len(task.Items) > 0 && task.Items[0].SourceBinding == nil {
		if _, err := os.Lstat(filepath.Join(service.Root, task.ID+".playback-binding.json")); !os.IsNotExist(err) {
			return fmt.Errorf("播放绑定记录不可降级")
		}
	}
	return nil
}
