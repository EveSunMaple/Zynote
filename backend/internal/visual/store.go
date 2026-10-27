package visual

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

const errCleanDirectoryInvalid = "画面清理目录无效"

type Service struct {
	Root                string
	FramesRoot          string
	Resources           string
	Platform            string
	AllowSystemFallback bool
}

func New(root, framesRoot, resources, platform string, allowSystemFallback bool) *Service {
	return &Service{
		Root: root, FramesRoot: framesRoot, Resources: resources,
		Platform: platform, AllowSystemFallback: allowSystemFallback,
	}
}

func (service *Service) List() ([]Task, error) {
	if err := os.MkdirAll(service.Root, 0o755); err != nil {
		return nil, badRequest("画面任务目录不可用")
	}
	entries, err := os.ReadDir(service.Root)
	if err != nil {
		return nil, badRequest("画面任务目录不可用")
	}
	result := []Task{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !idPattern.MatchString(strings.TrimSuffix(name, ".json")) {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		task, err := service.load(id)
		if err != nil {
			continue
		}
		output, outputErr := service.outputPath(task)
		if outputErr != nil {
			task.OutputRecovery = "changed"
			result = append(result, task)
			continue
		}
		current, digestErr := service.optionalDigest(output)
		if digestErr != nil {
			task.OutputRecovery = "changed"
		} else {
			task.OutputRecovery = OutputRecoveryReason(task, current, current != "", task.Output == "" && current != "")
		}
		result = append(result, task)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, nil
}

func (service *Service) Create(ctx context.Context, source string, limit int, policy, connection string) (Task, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return Task{}, badRequest(errSourceUnavailable)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Task{}, badRequest(errSourceUnavailable)
	}
	hash, err := fileDigest(absolute)
	if err != nil {
		return Task{}, badRequest(errSourceUnavailable)
	}
	task := Task{
		ID: strings.ReplaceAll(ids.New(), "-", ""), Source: absolute, SourceHash: hash,
		Frames: []Frame{}, Gaps: []any{}, Connection: connection,
		Preparation: &Preparation{Cap: limit, Policy: policy, Status: "等待提取"},
	}
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return service.Prepare(ctx, task.ID, connection)
}

func (service *Service) Prepare(ctx context.Context, id, connection string) (Task, error) {
	task, err := service.load(id)
	if err != nil {
		return Task{}, err
	}
	if task.Connection != connection {
		return Task{}, badRequest("连接已变化，请重新建立任务")
	}
	if task.Preparation == nil {
		return Task{}, badRequest("任务没有待提取记录")
	}
	if len(task.Frames) > 0 {
		return task, nil
	}
	hash, err := fileDigest(task.Source)
	if err != nil || hash != task.SourceHash {
		return Task{}, badRequest("原文件已改变，请重新建立任务")
	}
	if ctx.Err() != nil {
		return Task{}, ctx.Err()
	}
	task.Preparation.Status = "提取中"
	task.Preparation.Error = ""
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	captured, err := service.Capture(ctx, CaptureRequest{
		ID: task.ID, Path: task.Source, ExpectedHash: task.SourceHash,
		Cap: task.Preparation.Cap, Policy: task.Preparation.Policy,
	})
	if err != nil {
		task.Preparation.Status = "等待继续"
		if ctx.Err() != nil {
			task.Preparation.Error = "任务已取消"
		} else {
			task.Preparation.Error = err.Error()
		}
		_ = service.save(task)
		return Task{}, err
	}
	if ctx.Err() != nil {
		task.Preparation.Status = "等待继续"
		task.Preparation.Error = "任务已取消"
		_ = service.save(task)
		return Task{}, ctx.Err()
	}
	task.Frames = []Frame{}
	for _, frame := range captured.Frames {
		frame.Status = StatusWaiting
		task.Frames = append(task.Frames, frame)
	}
	task.Duration = captured.Duration
	task.Gaps = captured.Gaps
	task.Preparation.Status = "已提取"
	task.Preparation.Error = ""
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

type RecognizeFunc func(context.Context, Frame) (string, error)

func (service *Service) Recognize(
	ctx context.Context,
	id string,
	connection string,
	recognize RecognizeFunc,
	progress func(string),
) (Task, error) {
	task, err := service.load(id)
	if err != nil {
		return Task{}, err
	}
	if task.Connection != connection {
		return Task{}, badRequest("识别连接已有改变，请用当前配置新建任务；旧成果保留")
	}
	if len(task.Frames) == 0 {
		return Task{}, badRequest("请先完成画面提取")
	}
	if err := service.recoverJournal(&task); err != nil {
		return Task{}, err
	}
	if task.Output != "" {
		output, _ := service.outputPath(task)
		if current, err := service.optionalDigest(output); err != nil || current != task.OutputHash {
			return Task{}, badRequest("识别成果已改变，保留当前文件，请重新建立任务")
		}
	}
	sourceCheck := func() error {
		if hash, err := fileDigest(task.Source); err != nil || hash != task.SourceHash {
			return badRequest("源文件已经改变，请新建画面识别任务")
		}
		return nil
	}
	if err := sourceCheck(); err != nil {
		return Task{}, err
	}
	task.OutputPending = true
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	for index := range task.Frames {
		if err := ctx.Err(); err != nil {
			return Task{}, err
		}
		frame := &task.Frames[index]
		if frame.Status == StatusDone {
			continue
		}
		if progress != nil {
			progress("识别画面 " + itoa(index+1) + "/" + itoa(len(task.Frames)))
		}
		if hash, err := fileDigest(frame.Path); err != nil || hash != frame.Hash {
			frame.Status = StatusFailed
			frame.Error = "所选画面已改变"
			frame.Text = ""
			if err := service.save(task); err != nil {
				return Task{}, err
			}
			continue
		}
		text, err := recognize(ctx, *frame)
		if err != nil {
			if ctx.Err() != nil {
				return Task{}, ctx.Err()
			}
			frame.Status = StatusFailed
			frame.Error = err.Error()
			frame.Text = ""
			if err := service.save(task); err != nil {
				return Task{}, err
			}
			continue
		}
		if ctx.Err() != nil {
			return Task{}, ctx.Err()
		}
		if err := sourceCheck(); err != nil {
			return Task{}, err
		}
		if strings.TrimSpace(text) == "" {
			frame.Status = StatusFailed
			frame.Error = "识别未返回文字"
			frame.Text = ""
		} else {
			frame.Text = text
			frame.Status = StatusDone
			frame.Error = ""
		}
		if err := service.save(task); err != nil {
			return Task{}, err
		}
	}
	if err := sourceCheck(); err != nil {
		return Task{}, err
	}
	if err := service.commitOutput(&task, outputText(&task)); err != nil {
		return Task{}, err
	}
	failed := false
	for _, frame := range task.Frames {
		if frame.Status != StatusDone {
			failed = true
			break
		}
	}
	if !failed {
		if err := service.cleanupFrames(task.ID); err != nil {
			return Task{}, err
		}
		task.Cleaned = true
		if err := service.save(task); err != nil {
			return Task{}, err
		}
	}
	return task, nil
}

func (service *Service) RecoverOutput(id string) (Task, error) {
	task, err := service.load(id)
	if err != nil {
		return Task{}, err
	}
	if task.OutputJournal != nil {
		if err := service.recoverJournal(&task); err != nil {
			return Task{}, err
		}
		return task, nil
	}
	completed := false
	for _, frame := range task.Frames {
		if frame.Status == StatusDone {
			completed = true
			break
		}
	}
	if !completed {
		return Task{}, badRequest("没有已保存的画面识别文字可恢复")
	}
	output, _ := service.outputPath(task)
	current, err := service.optionalDigest(output)
	if err != nil {
		return Task{}, err
	}
	tracked := task.Output != "" && task.OutputHash != "" && current == task.OutputHash
	if (!tracked && current != "") || (task.Output != "" && current == "") {
		task.Output = filepath.Join(service.Root, task.ID+"-画面识别-恢复-"+strings.ReplaceAll(ids.New(), "-", "")+".md")
		task.OutputHash = ""
	}
	task.OutputPending = true
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	if err := service.commitOutput(&task, outputText(&task)); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (service *Service) cleanupFrames(id string) error {
	if !idPattern.MatchString(id) {
		return badRequest(errCleanDirectoryInvalid)
	}
	base, err := filepath.Abs(filepath.Join(service.FramesRoot, "native-visual"))
	if err != nil {
		return badRequest(errCleanDirectoryInvalid)
	}
	directory, err := filepath.Abs(filepath.Join(base, id))
	if err != nil {
		return badRequest(errCleanDirectoryInvalid)
	}
	relative, err := filepath.Rel(base, directory)
	if err != nil || relative != id {
		return badRequest(errCleanDirectoryInvalid)
	}
	if err := os.RemoveAll(directory); err != nil {
		return badRequest("画面清理失败")
	}
	return nil
}
