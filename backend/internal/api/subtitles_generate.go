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

type correctionRun struct {
	inv       *Invoker
	ctx       context.Context
	store     contract.StateStore
	state     *contract.State
	write     *storage.WriteDB
	lectureID string
	mode      learning.CorrectionMode

	lecture          map[string]any
	source           documents.SplitResult
	sourceSegments   []documents.Segment
	prompt           string
	profile          config.RuntimeProfile
	pptPath          string
	pptData          []byte
	ppt              string
	courseware       documents.CoursewareResult
	limitations      []string
	storedPpt        string
	selectedText     string
	modelLimitations []string
	fingerprint      string
	rows             []learning.CorrectedSegment
	resumed          int
	batches          []learning.CorrectionBatch
	contexts         []learning.CorrectionContext

	started       time.Time
	retries       int64
	cooldownMu    sync.Mutex
	cooldownUntil time.Time
	activeMu      sync.Mutex
	active        map[int]string
	resultMu      sync.Mutex
	client        *ai.Client
}

func (run *correctionRun) recoverStatus(err *error) {
	if *err == nil {
		return
	}
	aborted := run.ctx.Err() != nil || errors.Is(*err, context.Canceled)
	checkpoint := (*learning.CorrectionCheckpoint)(nil)
	savedFingerprint, savedTotal, savedRaw, checkpointErr := run.write.CorrectionCheckpoint(run.lectureID)
	if checkpointErr == nil && savedFingerprint != "" {
		if segments, parseErr := learning.ParseCorrectedSegments(savedRaw); parseErr == nil {
			checkpoint = &learning.CorrectionCheckpoint{
				Fingerprint: savedFingerprint, Total: savedTotal, Segments: segments,
			}
		}
	}
	var resumable []learning.CorrectedSegment
	if run.fingerprint != "" {
		resumable = learning.ResumableCorrection(checkpoint, run.fingerprint, run.sourceSegments)
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
	_ = run.write.SetStatus(run.lectureID, status)
	message := errorMessage(*err)
	switch {
	case resumable != nil && !aborted:
		*err = fmt.Errorf("已保存 %d/%d 条，点击继续校订可从断点恢复。%s", len(resumable), len(run.sourceSegments), message)
	case checkpoint != nil && run.fingerprint != "" && !aborted:
		*err = fmt.Errorf("%s 旧断点与当前资料或策略不匹配，本次没有可恢复的进度。", message)
	}
}

func (run *correctionRun) prepareSource() error {
	transcriptPath := storage.ToStringValue(run.lecture["transcript_path"])
	if transcriptPath == "" {
		return contract.InputInvalid("请先为本课次导入智云语音识别原文")
	}
	run.pptPath = storage.ToStringValue(run.lecture["ppt_path"])
	if run.pptPath == "" {
		return contract.InputInvalid("请先关联本课次的 PPT/课件，再校订字幕")
	}
	source, err := documents.ReadTranscript(run.inv.native, transcriptPath)
	if err != nil {
		return err
	}
	run.source = source
	run.sourceSegments = source.Segments
	run.inv.reportCorrection(run.lectureID, "准备资料", "准备语音原文与课件，一起交给模型校订", 0, 0)
	prompt, err := config.Prompt(run.state.DataRoot, run.state.PromptsRoot, "correction")
	if err != nil {
		return err
	}
	run.prompt = prompt
	profile, err := config.ActiveProfile(run.state.DataRoot, run.store.Reverse())
	if err != nil {
		return err
	}
	run.profile = profile
	return nil
}

func (run *correctionRun) prepareCourseware() error {
	run.inv.reportCorrection(run.lectureID, "本地解析课件", "提取页面文字与有限嵌入图片；不会上传原始 PPT/PDF 文件", 0, 0)
	pptData, err := os.ReadFile(run.pptPath)
	if err != nil {
		return fmt.Errorf("课件文件不可读")
	}
	run.pptData = pptData
	coursewareResult, err := documents.ParseCourseware(run.inv.native, run.pptPath)
	if err != nil {
		return err
	}
	run.courseware = coursewareResult
	identity, err := courseware.ReadSourceIdentity(run.pptPath)
	if err != nil {
		return err
	}
	pptText := storage.ToStringValue(run.lecture["ppt_text"])
	cached := courseware.ParseCacheWithIdentity(pptText, &identity)
	preserve := strings.TrimSpace(cached.Text) != "" &&
		(cached.State != "verified" || cached.Provenance == "manual" || cached.Provenance == "ocr")
	selectedText := coursewareResult.Text
	if preserve {
		selectedText = cached.Text
	}
	run.limitations = correctionLimitations(coursewareResult, cached, preserve)
	run.modelLimitations = uniqueStrings(run.limitations)
	run.ppt = coursewarePrompt(selectedText, run.limitations)
	run.storedPpt = pptText
	if !preserve {
		storedPpt, err := courseware.SerializeSourceCache(
			run.pptPath, coursewareResult.Text, "local", coursewareResult.NeedsOcr, coursewareResult.Omissions,
		)
		if err != nil {
			return err
		}
		run.storedPpt = storedPpt
		if err := run.write.SetLecturePptText(run.lectureID, storedPpt); err != nil {
			return err
		}
	}
	run.selectedText = selectedText
	return nil
}

func (run *correctionRun) loadCheckpoint() (*learning.CorrectionCheckpoint, string, error) {
	fingerprint, total, raw, err := run.write.CorrectionCheckpoint(run.lectureID)
	if err != nil {
		return nil, "", err
	}
	if fingerprint == "" {
		return nil, "", nil
	}
	segments, parseErr := learning.ParseCorrectedSegments(raw)
	if parseErr != nil {
		return nil, fingerprint, nil
	}
	return &learning.CorrectionCheckpoint{Fingerprint: fingerprint, Total: total, Segments: segments}, fingerprint, nil
}

func correctionOutputBudget(profile config.RuntimeProfile, mode learning.CorrectionMode, imageCount int) int {
	models := []string{profile.Model}
	if mode != learning.CorrectionText && imageCount > 0 {
		models = append(models, profile.VisionModel)
	}
	budget := 8192
	for _, model := range models {
		parameters, ok := profile.Parameters[model]
		if ok && parameters.MaxTokens != nil && *parameters.MaxTokens < budget {
			budget = *parameters.MaxTokens
		}
	}
	return budget
}

func (run *correctionRun) preparePlan() error {
	run.fingerprint = learning.CorrectionFingerprint(
		run.profile.Fingerprint, run.sourceSegments, run.prompt, run.pptData, run.selectedText, run.limitations, run.mode,
	)
	checkpoint, checkpointFingerprint, err := run.loadCheckpoint()
	if err != nil {
		return err
	}
	resumable := learning.ResumableCorrection(checkpoint, run.fingerprint, run.sourceSegments)
	if resumable != nil {
		run.rows = append(run.rows, resumable...)
	}
	switch {
	case len(run.rows) > 0:
		run.inv.reportCorrection(
			run.lectureID, "继续校订",
			fmt.Sprintf("已恢复 %d/%d 条，从未完成的批次继续", len(run.rows), len(run.sourceSegments)), 0, 0,
		)
	case checkpointFingerprint != "":
		run.inv.reportCorrection(run.lectureID, "重新校订", "资料、模式或断点校验发生变化，本次重新校订；旧断点将在首批成功后替换", 0, 0)
	}
	batches, err := learning.PlanCorrection(run.sourceSegments, len(run.rows), correctionOutputBudget(run.profile, run.mode, len(run.courseware.Images)))
	if err != nil {
		return err
	}
	run.batches = batches
	images := make([]learning.CoursewareImage, 0, len(run.courseware.Images))
	for _, image := range run.courseware.Images {
		images = append(images, learning.CoursewareImage{Label: image.Label, URL: image.URL})
	}
	contextFor := learning.CorrectionContextFor(run.ppt, images)
	run.contexts = make([]learning.CorrectionContext, len(batches))
	for index, batch := range batches {
		run.contexts[index] = contextFor(batch, run.sourceSegments, run.mode)
	}
	if learning.CorrectionNeedsVision(run.contexts, run.profile.VisionModel) {
		return contract.InputInvalid(
			"当前课件含嵌入图片，所选校订策略需要视觉模型。请先在设置中选择视觉模型，或改用“快速文字”策略。",
		)
	}
	return nil
}

func (run *correctionRun) savedCount() int {
	run.resultMu.Lock()
	defer run.resultMu.Unlock()
	return len(run.rows)
}

func (run *correctionRun) reportHeartbeat() {
	run.activeMu.Lock()
	if len(run.active) == 0 {
		run.activeMu.Unlock()
		return
	}
	names := make([]string, 0, len(run.active))
	for index := 0; index < len(run.batches); index++ {
		if name, ok := run.active[index]; ok {
			names = append(names, name)
		}
	}
	run.activeMu.Unlock()
	run.inv.reportCorrection(
		run.lectureID, "校订字幕",
		fmt.Sprintf(
			"%s；已保存 %d/%d 条；累计 %d 秒；重试 %d",
			strings.Join(names, "；"), run.savedCount(), len(run.sourceSegments),
			int(math.Round(time.Since(run.started).Seconds())), atomic.LoadInt64(&run.retries),
		), 0, 0,
	)
}

func (run *correctionRun) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run.reportHeartbeat()
		}
	}
}

func (run *correctionRun) waitCooldown(ctx context.Context) error {
	for {
		run.cooldownMu.Lock()
		target := run.cooldownUntil
		run.cooldownMu.Unlock()
		wait := time.Until(target)
		if wait <= 0 {
			return nil
		}
		if err := sleepContext(ctx, wait); err != nil {
			return err
		}
	}
}

func correctionRequestFor(run *correctionRun, batch learning.CorrectionBatch, index int, batchImages []learning.CoursewareImage) (correctionRequest, []ai.Image) {
	requestImages := make([]ai.Image, 0, len(batchImages))
	for _, image := range batchImages {
		requestImages = append(requestImages, ai.Image{Label: image.Label, URL: image.URL})
	}
	requestRows := make([]correctionRequestRow, 0, len(batch.Rows))
	for offset, row := range batch.Rows {
		requestRows = append(requestRows, correctionRequestRow{ID: batch.Start + offset, Text: row.Text})
	}
	requestLimitations := append([]string{}, run.modelLimitations...)
	requestLimitations = append(requestLimitations, run.contexts[index].Omissions...)
	input := correctionRequest{
		Courseware: run.contexts[index].Courseware, CoursewareLimitations: requestLimitations,
		LectureContext: run.contexts[index].LectureContext, Instructions: correctionInstructions, Rows: requestRows,
	}
	return input, requestImages
}

func (run *correctionRun) retryBatch(ctx context.Context, callErr error, attempt, index int) error {
	if ctx.Err() != nil || attempt == 2 || !ai.IsRetryable(callErr) {
		return callErr
	}
	count := atomic.AddInt64(&run.retries, 1)
	retryAfter := ai.RetryAfter(callErr)
	if retryAfter > 5*time.Minute {
		return fmt.Errorf("服务要求等待超过 5 分钟，已暂停校订，请稍后从断点继续")
	}
	wait := time.Second
	if attempt > 0 {
		wait = 3 * time.Second
	}
	if retryAfter > wait {
		wait = retryAfter
	}
	run.cooldownMu.Lock()
	target := time.Now().Add(wait)
	if target.After(run.cooldownUntil) {
		run.cooldownUntil = target
	}
	run.cooldownMu.Unlock()
	run.inv.reportCorrection(
		run.lectureID, "连接重试",
		fmt.Sprintf(
			"批次 %d 暂停 %d 秒后重试 %d/2；累计重试 %d；已保存 %d 条",
			index+1, int(math.Ceil(wait.Seconds())), attempt+1, count, run.savedCount(),
		), index+1, len(run.batches),
	)
	return sleepContext(ctx, wait)
}

func (run *correctionRun) runBatch(ctx context.Context, batch learning.CorrectionBatch, index int) ([]learning.CorrectedSegment, error) {
	correctionContext := run.contexts[index]
	batchImages := correctionContext.Images
	model := run.profile.Model
	if len(batchImages) > 0 {
		model = strings.TrimSpace(run.profile.VisionModel)
	}
	batchStarted := time.Now()
	run.activeMu.Lock()
	run.active[index] = fmt.Sprintf("批次 %d/%d，模型 %s，图片 %d", index+1, len(run.batches), model, len(batchImages))
	run.activeMu.Unlock()
	defer func() {
		run.activeMu.Lock()
		delete(run.active, index)
		run.activeMu.Unlock()
	}()
	input, requestImages := correctionRequestFor(run, batch, index, batchImages)
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := run.waitCooldown(ctx); err != nil {
			return nil, err
		}
		run.inv.reportCorrection(
			run.lectureID, "校订字幕",
			fmt.Sprintf(
				"批次 %d/%d，%d–%d 条；模型 %s；图片 %d；重试 %d；累计 %d 秒",
				index+1, len(run.batches), batch.Start+1, batch.Start+len(batch.Rows), model,
				len(batchImages), atomic.LoadInt64(&run.retries),
				int(math.Round(time.Since(run.started).Seconds())),
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
		_, callErr := run.client.JSONWithOptions(
			ctx, run.prompt, input, validate,
			ai.JSONOptions{
				Images: requestImages,
				OnRetry: func() {
					count := atomic.AddInt64(&run.retries, 1)
					run.inv.reportCorrection(
						run.lectureID, "格式重试",
						fmt.Sprintf("批次 %d 输出格式未通过校验，重试一次；累计重试 %d", index+1, count),
						index+1, len(run.batches),
					)
				},
			},
		)
		if callErr == nil {
			run.inv.reportCorrection(
				run.lectureID, "批次返回",
				fmt.Sprintf(
					"批次 %d，模型 %s，图片 %d，耗时 %d 秒；等待按原文顺序保存",
					index+1, model, len(batchImages), int(math.Round(time.Since(batchStarted).Seconds())),
				), index+1, len(run.batches),
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
		if retryErr := run.retryBatch(ctx, callErr, attempt, index); retryErr != nil {
			return nil, retryErr
		}
	}
	return nil, fmt.Errorf("当前批次未返回校订结果")
}

func (run *correctionRun) commitBatch(corrected []learning.CorrectedSegment, batch learning.CorrectionBatch) error {
	run.resultMu.Lock()
	next := append(append([]learning.CorrectedSegment{}, run.rows...), corrected...)
	run.resultMu.Unlock()
	if err := run.write.SaveCorrectionBatch(
		run.lectureID, run.fingerprint, len(run.sourceSegments), next, len(next),
	); err != nil {
		return err
	}
	run.resultMu.Lock()
	run.rows = next
	run.resultMu.Unlock()
	elapsed := time.Since(run.started).Seconds()
	completed := len(next) - run.resumed
	eta := 0
	if completed > 0 {
		eta = int(math.Round(elapsed * float64(len(run.sourceSegments)-len(next)) / float64(completed)))
	}
	run.inv.reportCorrection(
		run.lectureID, "片段完成",
		fmt.Sprintf(
			"已保存 %d/%d 条；累计 %d 秒，预计剩余约 %d 秒（仅按本次速度估算）；重试 %d；可中断后继续",
			len(next), len(run.sourceSegments), int(math.Round(elapsed)), eta,
			atomic.LoadInt64(&run.retries),
		), len(next), len(run.sourceSegments),
	)
	return nil
}

func (run *correctionRun) finish() (any, error) {
	if err := run.ctx.Err(); err != nil {
		return nil, err
	}
	run.resultMu.Lock()
	final := append([]learning.CorrectedSegment{}, run.rows...)
	run.resultMu.Unlock()
	courseID := storage.ToStringValue(run.lecture["course_id"])
	folder := filepath.Join(run.state.DataRoot, "courses", courseID, "transcript-results", run.lectureID)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, fmt.Errorf("字幕结果目录不可用")
	}
	srt := learning.ToSrt(learning.CorrectedSegmentsToDocuments(final))
	if err := os.WriteFile(filepath.Join(folder, "校订字幕.srt"), []byte(srt), 0o644); err != nil {
		return nil, fmt.Errorf("字幕结果写入失败")
	}
	if err := run.write.UpdateLectureCorrection(
		run.lectureID, run.source.Text, boolToInt(run.source.Estimated), run.storedPpt,
	); err != nil {
		return nil, err
	}
	if err := run.write.SaveSubtitles(run.lectureID, srt, final); err != nil {
		return nil, err
	}
	message := "原文与 PPT 校订完成；疑点及原文已保留"
	if run.source.Estimated {
		message += "，时间为估算"
	} else {
		message += "，保留原文时间点"
	}
	run.inv.reportCorrection(run.lectureID, "完成", message, 0, 0)
	return run.inv.lectureDetailOf(run.state, run.lectureID)
}

func (run *correctionRun) execute() (any, error) {
	run.started = time.Now()
	run.resumed = len(run.rows)
	run.inv.reportCorrection(
		run.lectureID, "校订计划",
		fmt.Sprintf(
			"%s；%d 批，最多 2 批并发；输出预算按模型设置或保守默认估算",
			run.mode.Label(), len(run.batches),
		), 0, 0,
	)
	run.client = ai.New(ai.Profile{
		BaseURL: run.profile.BaseURL, Model: run.profile.Model, VisionModel: run.profile.VisionModel,
		APIFormat: run.profile.APIFormat, Key: run.profile.Key, Parameters: run.profile.Parameters,
	})
	heartbeatCtx, stopHeartbeat := context.WithCancel(run.ctx)
	heartbeatDone := make(chan struct{})
	defer func() {
		stopHeartbeat()
		<-heartbeatDone
	}()
	go func() {
		defer close(heartbeatDone)
		run.heartbeat(heartbeatCtx)
	}()
	if err := learning.RunCorrectionBatches(run.ctx, run.batches, run.runBatch, run.commitBatch); err != nil {
		return nil, err
	}
	return run.finish()
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
	run := &correctionRun{
		inv: inv, ctx: ctx, store: store, state: state, write: write,
		lectureID: lectureID, mode: mode, lecture: lecture, active: map[int]string{},
	}
	defer run.recoverStatus(&err)
	if err := run.prepareSource(); err != nil {
		return nil, err
	}
	if err := run.prepareCourseware(); err != nil {
		return nil, err
	}
	if err := run.preparePlan(); err != nil {
		return nil, err
	}
	return run.execute()
}
