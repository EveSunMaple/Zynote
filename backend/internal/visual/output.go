package visual

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func fileDigest(path string) (string, error) {
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

func (service *Service) optionalDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", badRequest("识别成果路径类型无效，保留当前文件")
	}
	return fileDigest(path)
}

func (service *Service) commitOutput(task *Task, text string) error {
	output, err := service.outputPath(*task)
	if err != nil {
		return err
	}
	temp := output + ".pending-" + strings.ReplaceAll(ids.New(), "-", "")
	hash := digestText(text)
	previousHash := task.OutputHash
	current, err := service.optionalDigest(output)
	if err != nil {
		return err
	}
	if task.Output != "" && previousHash != "" && current != previousHash {
		return badRequest("识别成果已改变，保留当前文件，请重新建立任务")
	}
	if current != "" && (task.OutputHash == "" || current != task.OutputHash) {
		return badRequest("已有识别成果文件，保留当前文件")
	}
	backup := ""
	if previousHash != "" {
		backup = output + ".previous-" + strings.ReplaceAll(ids.New(), "-", "")
		if existing, err := service.optionalDigest(backup); err != nil {
			return err
		} else if existing != "" {
			return badRequest("识别成果备份路径已存在，保留当前文件")
		}
	}
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return badRequest("画面成果暂存文件写入失败，保留当前文件")
	}
	if _, err := file.WriteString(text); err != nil {
		file.Close()
		_ = os.Remove(temp)
		return badRequest("画面成果暂存文件写入失败，保留当前文件")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return badRequest("画面成果暂存文件写入失败，保留当前文件")
	}
	defer os.Remove(temp)
	if current, err := service.optionalDigest(temp); err != nil || current != hash {
		return badRequest("画面成果暂存校验失败，保留当前文件")
	}
	task.OutputJournal = &Journal{Output: output, Temp: temp, Hash: hash, PreviousHash: previousHash, Backup: backup}
	if err := service.save(*task); err != nil {
		return err
	}
	if current, err := service.optionalDigest(output); err != nil || current != previousHash {
		return badRequest("识别成果已改变，保留当前文件，替换已中止")
	}
	if err := service.replaceTemp(temp, output, backup, previousHash, hash); err != nil {
		return err
	}
	if current, err := service.optionalDigest(output); err != nil || current != hash {
		return badRequest("画面成果替换核验失败，保留恢复记录")
	}
	task.Output = output
	task.OutputHash = hash
	task.OutputJournal = nil
	task.OutputPending = false
	if err := service.save(*task); err != nil {
		return err
	}
	if backup != "" {
		if value, err := service.optionalDigest(backup); err == nil && value == previousHash {
			_ = os.Remove(backup)
		}
	}
	return nil
}

func (service *Service) replaceTemp(temp, output, backup, previousHash, newHash string) error {
	if err := os.Rename(temp, output); err == nil {
		return nil
	}
	if backup == "" {
		return badRequest("识别成果替换失败，保留当前文件")
	}
	current, err := service.optionalDigest(output)
	if err != nil {
		return err
	}
	backupHash, err := service.optionalDigest(backup)
	if err != nil {
		return err
	}
	tempHash, err := service.optionalDigest(temp)
	if err != nil {
		return err
	}
	if current != previousHash || backupHash != "" || tempHash != newHash {
		return badRequest("识别成果替换前校验失败，保留当前文件")
	}
	if err := os.Rename(output, backup); err != nil {
		return badRequest("识别成果备份失败，保留恢复记录")
	}
	if value, err := service.optionalDigest(backup); err != nil || value != previousHash {
		return badRequest("识别成果备份校验失败，保留恢复记录")
	}
	if err := os.Rename(temp, output); err != nil {
		if value, err2 := service.optionalDigest(output); err2 == nil && value == "" {
			if backupValue, err3 := service.optionalDigest(backup); err3 == nil && backupValue == previousHash {
				_ = os.Rename(backup, output)
			}
		}
		return badRequest("识别成果替换失败，保留当前文件")
	}
	return nil
}

func (service *Service) recoverJournal(task *Task) error {
	journal := task.OutputJournal
	if journal == nil {
		return nil
	}
	output, err := service.outputPath(*task)
	if err != nil {
		return err
	}
	tempPrefix := output + ".pending-"
	backupPrefix := output + ".previous-"
	if journal.Output != output ||
		!strings.HasPrefix(journal.Temp, tempPrefix) ||
		!idPattern.MatchString(strings.TrimPrefix(journal.Temp, tempPrefix)) ||
		(journal.Backup != "" && (!strings.HasPrefix(journal.Backup, backupPrefix) ||
			!idPattern.MatchString(strings.TrimPrefix(journal.Backup, backupPrefix)))) ||
		!hashPattern.MatchString(journal.Hash) ||
		(journal.PreviousHash != "" && !hashPattern.MatchString(journal.PreviousHash)) {
		return badRequest("识别成果恢复记录无效，保留当前文件")
	}
	current, err := service.optionalDigest(output)
	if err != nil {
		return err
	}
	if current == journal.Hash {
		task.Output = output
		task.OutputHash = journal.Hash
		task.OutputJournal = nil
		task.OutputPending = false
		if err := service.save(*task); err != nil {
			return err
		}
		if journal.Backup != "" {
			if value, err := service.optionalDigest(journal.Backup); err == nil && value == journal.PreviousHash {
				_ = os.Remove(journal.Backup)
			}
		}
		if value, err := service.optionalDigest(journal.Temp); err == nil && value == journal.Hash {
			_ = os.Remove(journal.Temp)
		}
		return nil
	}
	if current != journal.PreviousHash && current != "" {
		return badRequest("识别成果已改变，保留当前文件，恢复已中止")
	}
	tempHash, err := service.optionalDigest(journal.Temp)
	if err != nil || tempHash != journal.Hash {
		return badRequest("识别成果暂存文件校验失败，保留当前文件以便恢复")
	}
	if current == "" && journal.PreviousHash != "" {
		if journal.Backup == "" {
			return badRequest("识别成果备份校验失败，保留当前文件以便恢复")
		}
		value, err := service.optionalDigest(journal.Backup)
		if err != nil || value != journal.PreviousHash {
			return badRequest("识别成果备份校验失败，保留当前文件以便恢复")
		}
	}
	if err := service.replaceTemp(journal.Temp, output, journal.Backup, journal.PreviousHash, journal.Hash); err != nil {
		return err
	}
	if current, err := service.optionalDigest(output); err != nil || current != journal.Hash {
		return badRequest("画面成果替换核验失败，保留恢复记录")
	}
	task.Output = output
	task.OutputHash = journal.Hash
	task.OutputJournal = nil
	task.OutputPending = false
	if err := service.save(*task); err != nil {
		return err
	}
	if journal.Backup != "" {
		if value, err := service.optionalDigest(journal.Backup); err == nil && value == journal.PreviousHash {
			_ = os.Remove(journal.Backup)
		}
	}
	return nil
}

func frameClock(seconds float64) string {
	minutes := int(seconds) / 60
	rest := int(seconds) % 60
	return fmt.Sprintf("%d:%02d", minutes, rest)
}

func outputText(task *Task) string {
	failed := []Frame{}
	for _, frame := range task.Frames {
		if frame.Status != StatusDone {
			failed = append(failed, frame)
		}
	}
	gapText := "- 提取器没有报告缺口；稀疏采样仍不代表识别了未选取的时段。"
	if len(task.Gaps) > 0 {
		lines := []string{}
		for _, gap := range task.Gaps {
			encoded, _ := json.Marshal(gap)
			lines = append(lines, "- "+string(encoded))
		}
		gapText = strings.Join(lines, "\n")
	}
	done := len(task.Frames) - len(failed)
	parts := []string{
		"# 画面文字与内容\n\n来源：" + task.Source + "\n源文件 SHA256：" + task.SourceHash +
			fmt.Sprintf("\n总时长：%.2f 秒\n\n仅识别所选的 %d/%d 个画面，不代表视频全部内容。", task.Duration, done, len(task.Frames)) +
			"\n\n## 抽样缺口\n\n" + gapText + "\n\n## 识别结果\n\n",
	}
	sections := []string{}
	for _, frame := range task.Frames {
		content := "未得到可用识别结果：" + frame.Error
		if frame.Status == StatusDone {
			content = frame.Text
		}
		sections = append(sections, "### "+frameClock(frame.Time)+" · "+frame.Status+"\n\n"+content+
			"\n\n画面 SHA256："+frame.Hash)
	}
	parts = append(parts, strings.Join(sections, "\n\n"))
	failures := []string{}
	for _, frame := range failed {
		message := frame.Error
		if message == "" {
			message = "仍待处理"
		}
		failures = append(failures, "- "+frameClock(frame.Time)+"："+message)
	}
	if len(failures) == 0 {
		failures = append(failures, "- 无")
	}
	parts = append(parts, "\n\n## 未完成项目\n\n"+strings.Join(failures, "\n"))
	return strings.Join(parts, "")
}
