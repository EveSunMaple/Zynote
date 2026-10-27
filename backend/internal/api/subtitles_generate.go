package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

const correctionInstructions = "结合择取的课件文字、图片和相邻原文校订。课件不是完整内容；无依据的图表、公式不可猜测。rows 必须逐条同序返回，lectureContext 不返回。未修改且无疑点时 reason 为空字符串；修改或不确定时保留简短依据。不得改变否定、术语、公式与考试语气。"

type correctionRequestRow struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type correctionRequest struct {
	Courseware            string                 `json:"courseware"`
	CoursewareLimitations []string               `json:"coursewareLimitations"`
	LectureContext        []learning.AdjacentRow `json:"lectureContext"`
	Instructions          string                 `json:"instructions"`
	Rows                  []correctionRequestRow `json:"rows"`
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func errorMessage(err error) string {
	var appErr *contract.AppError
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return err.Error()
}

func (inv *Invoker) reportCorrection(lectureID, stage, message string, part, total int) {
	data := map[string]any{"lectureId": lectureID, "stage": stage, "message": message}
	if total > 0 {
		data["part"] = part
		data["total"] = total
	}
	inv.emit(map[string]any{"kind": "progress", "data": data})
}

func correctionLimitations(coursewareResult documents.CoursewareResult, cached courseware.ParsedCache, preserve bool) []string {
	limitations := make([]string, 0, len(coursewareResult.Omissions)+4)
	limitations = append(limitations, coursewareResult.Omissions...)
	if preserve {
		limitations = append(limitations, courseware.Limitations(cached)...)
	}
	if coursewareResult.NeedsOcr {
		limitations = append(limitations, "扫描 PDF：本地没有提取到足够文字，未执行 OCR。")
	}
	return limitations
}

func coursewarePrompt(text string, limitations []string) string {
	if len(limitations) == 0 {
		return text
	}
	lines := make([]string, 0, len(limitations))
	for _, item := range uniqueStrings(limitations) {
		lines = append(lines, "- "+item)
	}
	return text + "\n\n[课件提取限制]\n" + strings.Join(lines, "\n")
}

func (inv *Invoker) handleSubtitlesGenerate(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID   string `json:"id"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	mode := learning.CorrectionQuality
	if input.Mode != "" {
		parsed, ok := learning.ParseCorrectionMode(input.Mode)
		if !ok {
			return nil, contract.InputInvalid("校订策略无效")
		}
		mode = parsed
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	return inv.runJob("lecture:"+input.ID, func(ctx context.Context) (any, error) {
		return inv.transcribeLecture(ctx, store, state, write, input.ID, mode)
	})
}

func (inv *Invoker) transcribeLecture(
	ctx context.Context,
	store contract.StateStore,
	state *contract.State,
	write *storage.WriteDB,
	lectureID string,
	mode learning.CorrectionMode,
) (result any, err error) {
	lecture, err := write.Lecture(lectureID)
	if err != nil {
		return nil, err
	}
	if err := write.SetStatus(lectureID, "校订中"); err != nil {
		return nil, err
	}
	var sourceSegments []documents.Segment
	var fingerprint string
	defer func() {
		if err == nil {
			return
		}
		aborted := ctx.Err() != nil || errors.Is(err, context.Canceled)
		checkpoint := (*learning.CorrectionCheckpoint)(nil)
		savedFingerprint, savedTotal, savedRaw, checkpointErr := write.CorrectionCheckpoint(lectureID)
		if checkpointErr == nil && savedFingerprint != "" {
			if segments, parseErr := learning.ParseCorrectedSegments(savedRaw); parseErr == nil {
				checkpoint = &learning.CorrectionCheckpoint{
					Fingerprint: savedFingerprint, Total: savedTotal, Segments: segments,
				}
			}
		}
		var resumable []learning.CorrectedSegment
		if fingerprint != "" {
			resumable = learning.ResumableCorrection(checkpoint, fingerprint, sourceSegments)
		}
		status := "需核对"
		switch {
		case resumable != nil && aborted:
			status = "已取消，可继续"
		case resumable != nil:
			status = "校订未完成，可继续"
		case aborted:
			status = "已取消"
		}
		_ = write.SetStatus(lectureID, status)
		message := errorMessage(err)
		switch {
		case resumable != nil && !aborted:
			err = fmt.Errorf("已保存 %d/%d 条，点击继续校订可从断点恢复。%s", len(resumable), len(sourceSegments), message)
		case checkpoint != nil && fingerprint != "" && !aborted:
			err = fmt.Errorf("%s 旧断点与当前资料或策略不匹配，本次没有可恢复的进度。", message)
		}
	}()
	transcriptPath := storage.ToStringValue(lecture["transcript_path"])
	if transcriptPath == "" {
		return nil, contract.InputInvalid("请先为本课次导入智云语音识别原文")
	}
	pptPath := storage.ToStringValue(lecture["ppt_path"])
	if pptPath == "" {
		return nil, contract.InputInvalid("请先关联本课次的 PPT/课件，再校订字幕")
	}
	source, err := documents.ReadTranscript(inv.native, transcriptPath)
	if err != nil {
		return nil, err
	}
	sourceSegments = source.Segments
	inv.reportCorrection(lectureID, "准备资料", "准备语音原文与课件，一起交给模型校订", 0, 0)
	prompt, err := config.Prompt(state.DataRoot, state.PromptsRoot, "correction")
	if err != nil {
		return nil, err
	}
	profile, err := config.ActiveProfile(state.DataRoot, store.Reverse())
	if err != nil {
		return nil, err
	}
	inv.reportCorrection(lectureID, "本地解析课件", "提取页面文字与有限嵌入图片；不会上传原始 PPT/PDF 文件", 0, 0)
	pptData, err := os.ReadFile(pptPath)
	if err != nil {
		return nil, fmt.Errorf("课件文件不可读")
	}
	coursewareResult, err := documents.ParseCourseware(inv.native, pptPath)
	if err != nil {
		return nil, err
	}
	identity, err := courseware.ReadSourceIdentity(pptPath)
	if err != nil {
		return nil, err
	}
	pptText := storage.ToStringValue(lecture["ppt_text"])
	cached := courseware.ParseCacheWithIdentity(pptText, &identity)
	preserve := strings.TrimSpace(cached.Text) != "" &&
		(cached.State != "verified" || cached.Provenance == "manual" || cached.Provenance == "ocr")
	selectedText := coursewareResult.Text
	if preserve {
		selectedText = cached.Text
	}
	limitations := correctionLimitations(coursewareResult, cached, preserve)
	ppt := coursewarePrompt(selectedText, limitations)
	storedPpt := pptText
	if !preserve {
		storedPpt, err = courseware.SerializeSourceCache(
			pptPath, coursewareResult.Text, "local", coursewareResult.NeedsOcr, coursewareResult.Omissions,
		)
		if err != nil {
			return nil, err
		}
		if err := write.SetLecturePptText(lectureID, storedPpt); err != nil {
			return nil, err
		}
	}
	modelLimitations := uniqueStrings(limitations)
	fingerprint = learning.CorrectionFingerprint(
		profile.Fingerprint, sourceSegments, prompt, pptData, selectedText, limitations, mode,
	)
	checkpoint := (*learning.CorrectionCheckpoint)(nil)
	checkpointFingerprint, checkpointTotal, checkpointRaw, err := write.CorrectionCheckpoint(lectureID)
	if err != nil {
		return nil, err
	}
	if checkpointFingerprint != "" {
		if segments, parseErr := learning.ParseCorrectedSegments(checkpointRaw); parseErr == nil {
			checkpoint = &learning.CorrectionCheckpoint{
				Fingerprint: checkpointFingerprint, Total: checkpointTotal, Segments: segments,
			}
		}
	}
	resumable := learning.ResumableCorrection(checkpoint, fingerprint, sourceSegments)
	rows := []learning.CorrectedSegment{}
	if resumable != nil {
		rows = append(rows, resumable...)
	}
	switch {
	case len(rows) > 0:
		inv.reportCorrection(
			lectureID, "继续校订",
			fmt.Sprintf("已恢复 %d/%d 条，从未完成的批次继续", len(rows), len(sourceSegments)), 0, 0,
		)
	case checkpointFingerprint != "":
		inv.reportCorrection(lectureID, "重新校订", "资料、模式或断点校验发生变化，本次重新校订；旧断点将在首批成功后替换", 0, 0)
	}
	models := []string{profile.Model}
	if mode != learning.CorrectionText && len(coursewareResult.Images) > 0 {
		models = append(models, profile.VisionModel)
	}
	outputBudget := 8192
	for _, model := range models {
		parameters, ok := profile.Parameters[model]
		if ok && parameters.MaxTokens != nil && *parameters.MaxTokens < outputBudget {
			outputBudget = *parameters.MaxTokens
		}
	}
	batches, err := learning.PlanCorrection(sourceSegments, len(rows), outputBudget)
	if err != nil {
		return nil, err
	}
	images := make([]learning.CoursewareImage, 0, len(coursewareResult.Images))
	for _, image := range coursewareResult.Images {
		images = append(images, learning.CoursewareImage{Label: image.Label, URL: image.URL})
	}
	contextFor := learning.CorrectionContextFor(ppt, images)
	contexts := make([]learning.CorrectionContext, len(batches))
	for index, batch := range batches {
		contexts[index] = contextFor(batch, sourceSegments, mode)
	}
	if learning.CorrectionNeedsVision(contexts, profile.VisionModel) {
		return nil, contract.InputInvalid(
			"当前课件含嵌入图片，所选校订策略需要视觉模型。请先在设置中选择视觉模型，或改用“快速文字”策略。",
		)
	}
	started := time.Now()
	resumed := len(rows)
	var retries int64
	var resultMu sync.Mutex
	var cooldownMu sync.Mutex
	var cooldownUntil time.Time
	var activeMu sync.Mutex
	active := map[int]string{}
	savedCount := func() int {
		resultMu.Lock()
		defer resultMu.Unlock()
		return len(rows)
	}
	inv.reportCorrection(
		lectureID, "校订计划",
		fmt.Sprintf(
			"%s；%d 批，最多 2 批并发；输出预算按模型设置或保守默认估算",
			mode.Label(), len(batches),
		), 0, 0,
	)
	heartbeat, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	heartbeatDone := make(chan struct{})
	defer func() {
		stopHeartbeat()
		<-heartbeatDone
	}()
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeat.Done():
				return
			case <-ticker.C:
				activeMu.Lock()
				if len(active) == 0 {
					activeMu.Unlock()
					continue
				}
				names := make([]string, 0, len(active))
				for index := 0; index < len(batches); index++ {
					if name, ok := active[index]; ok {
						names = append(names, name)
					}
				}
				activeMu.Unlock()
				inv.reportCorrection(
					lectureID, "校订字幕",
					fmt.Sprintf(
						"%s；已保存 %d/%d 条；累计 %d 秒；重试 %d",
						strings.Join(names, "；"), savedCount(), len(sourceSegments),
						int(math.Round(time.Since(started).Seconds())), atomic.LoadInt64(&retries),
					), 0, 0,
				)
			}
		}
	}()
	client := ai.New(ai.Profile{
		BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
		APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
	})
	waitCooldown := func(batchContext context.Context) error {
		for {
			cooldownMu.Lock()
			target := cooldownUntil
			cooldownMu.Unlock()
			wait := time.Until(target)
			if wait <= 0 {
				return nil
			}
			if err := sleepContext(batchContext, wait); err != nil {
				return err
			}
		}
	}
	err = learning.RunCorrectionBatches(
		ctx, batches,
		func(batchContext context.Context, batch learning.CorrectionBatch, index int) ([]learning.CorrectedSegment, error) {
			correctionContext := contexts[index]
			batchImages := correctionContext.Images
			model := profile.Model
			if len(batchImages) > 0 {
				model = strings.TrimSpace(profile.VisionModel)
			}
			batchStarted := time.Now()
			activeMu.Lock()
			active[index] = fmt.Sprintf("批次 %d/%d，模型 %s，图片 %d", index+1, len(batches), model, len(batchImages))
			activeMu.Unlock()
			requestImages := make([]ai.Image, 0, len(batchImages))
			for _, image := range batchImages {
				requestImages = append(requestImages, ai.Image{Label: image.Label, URL: image.URL})
			}
			requestRows := make([]correctionRequestRow, 0, len(batch.Rows))
			for offset, row := range batch.Rows {
				requestRows = append(requestRows, correctionRequestRow{ID: batch.Start + offset, Text: row.Text})
			}
			requestLimitations := append([]string{}, modelLimitations...)
			requestLimitations = append(requestLimitations, correctionContext.Omissions...)
			input := correctionRequest{
				Courseware: correctionContext.Courseware, CoursewareLimitations: requestLimitations,
				LectureContext: correctionContext.LectureContext, Instructions: correctionInstructions, Rows: requestRows,
			}
			defer func() {
				activeMu.Lock()
				delete(active, index)
				activeMu.Unlock()
			}()
			for attempt := 0; attempt < 3; attempt++ {
				if err := batchContext.Err(); err != nil {
					return nil, err
				}
				if err := waitCooldown(batchContext); err != nil {
					return nil, err
				}
				inv.reportCorrection(
					lectureID, "校订字幕",
					fmt.Sprintf(
						"批次 %d/%d，%d–%d 条；模型 %s；图片 %d；重试 %d；累计 %d 秒",
						index+1, len(batches), batch.Start+1, batch.Start+len(batch.Rows), model,
						len(batchImages), atomic.LoadInt64(&retries),
						int(math.Round(time.Since(started).Seconds())),
					), 0, 0,
				)
				var corrected []learning.CorrectedRow
				validate := func(value []byte) error {
					parsed, parseErr := learning.ValidateCorrectionRows(value, batch)
					if parseErr != nil {
						return parseErr
					}
					corrected = parsed
					return nil
				}
				_, callErr := client.JSONWithOptions(
					batchContext, prompt, input, validate,
					ai.JSONOptions{
						Images: requestImages,
						OnRetry: func() {
							count := atomic.AddInt64(&retries, 1)
							inv.reportCorrection(
								lectureID, "格式重试",
								fmt.Sprintf("批次 %d 输出格式未通过校验，重试一次；累计重试 %d", index+1, count),
								index+1, len(batches),
							)
						},
					},
				)
				if callErr == nil {
					inv.reportCorrection(
						lectureID, "批次返回",
						fmt.Sprintf(
							"批次 %d，模型 %s，图片 %d，耗时 %d 秒；等待按原文顺序保存",
							index+1, model, len(batchImages), int(math.Round(time.Since(batchStarted).Seconds())),
						), index+1, len(batches),
					)
					out := make([]learning.CorrectedSegment, len(batch.Rows))
					for offset, row := range batch.Rows {
						out[offset] = learning.CorrectedSegment{
							Start: row.Start, End: row.End, Text: corrected[offset].Text,
							OriginalText: row.Text, Uncertain: corrected[offset].Uncertain,
							Reason: corrected[offset].Reason,
						}
					}
					return out, nil
				}
				if batchContext.Err() != nil || attempt == 2 || !ai.IsRetryable(callErr) {
					return nil, callErr
				}
				count := atomic.AddInt64(&retries, 1)
				retryAfter := ai.RetryAfter(callErr)
				if retryAfter > 5*time.Minute {
					return nil, fmt.Errorf("服务要求等待超过 5 分钟，已暂停校订，请稍后从断点继续")
				}
				wait := time.Second
				if attempt > 0 {
					wait = 3 * time.Second
				}
				if retryAfter > wait {
					wait = retryAfter
				}
				cooldownMu.Lock()
				target := time.Now().Add(wait)
				if target.After(cooldownUntil) {
					cooldownUntil = target
				}
				cooldownMu.Unlock()
				inv.reportCorrection(
					lectureID, "连接重试",
					fmt.Sprintf(
						"批次 %d 暂停 %d 秒后重试 %d/2；累计重试 %d；已保存 %d 条",
						index+1, int(math.Ceil(wait.Seconds())), attempt+1, count, savedCount(),
					), index+1, len(batches),
				)
				if err := sleepContext(batchContext, wait); err != nil {
					return nil, err
				}
			}
			return nil, fmt.Errorf("当前批次未返回校订结果")
		},
		func(corrected []learning.CorrectedSegment, batch learning.CorrectionBatch) error {
			resultMu.Lock()
			next := append(append([]learning.CorrectedSegment{}, rows...), corrected...)
			resultMu.Unlock()
			if err := write.SaveCorrectionBatch(
				lectureID, fingerprint, len(sourceSegments), next, len(next),
			); err != nil {
				return err
			}
			resultMu.Lock()
			rows = next
			resultMu.Unlock()
			elapsed := time.Since(started).Seconds()
			completed := len(next) - resumed
			eta := 0
			if completed > 0 {
				eta = int(math.Round(elapsed * float64(len(sourceSegments)-len(next)) / float64(completed)))
			}
			inv.reportCorrection(
				lectureID, "片段完成",
				fmt.Sprintf(
					"已保存 %d/%d 条；累计 %d 秒，预计剩余约 %d 秒（仅按本次速度估算）；重试 %d；可中断后继续",
					len(next), len(sourceSegments), int(math.Round(elapsed)), eta,
					atomic.LoadInt64(&retries),
				), len(next), len(sourceSegments),
			)
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resultMu.Lock()
	final := append([]learning.CorrectedSegment{}, rows...)
	resultMu.Unlock()
	courseID := storage.ToStringValue(lecture["course_id"])
	folder := filepath.Join(state.DataRoot, "courses", courseID, "transcript-results", lectureID)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, fmt.Errorf("字幕结果目录不可用")
	}
	srt := learning.ToSrt(learning.CorrectedSegmentsToDocuments(final))
	if err := os.WriteFile(filepath.Join(folder, "校订字幕.srt"), []byte(srt), 0o644); err != nil {
		return nil, fmt.Errorf("字幕结果写入失败")
	}
	if err := write.UpdateLectureCorrection(
		lectureID, source.Text, boolToInt(source.Estimated), storedPpt,
	); err != nil {
		return nil, err
	}
	if err := write.SaveSubtitles(lectureID, srt, final); err != nil {
		return nil, err
	}
	message := "原文与 PPT 校订完成；疑点及原文已保留"
	if source.Estimated {
		message += "，时间为估算"
	} else {
		message += "，保留原文时间点"
	}
	inv.reportCorrection(lectureID, "完成", message, 0, 0)
	return inv.lectureDetailOf(state, lectureID)
}
