package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
)

type FileInput struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"`
	Path     string `json:"path,omitempty"`
}

type ContentPart struct {
	Type     string     `json:"type"`
	Text     string     `json:"text,omitempty"`
	File     *FileInput `json:"file,omitempty"`
	ImageURL *ImageURL  `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type Image struct {
	Label string
	URL   string
}

type RetryableError struct {
	Message    string
	RetryAfter time.Duration
}

func (value *RetryableError) Error() string { return value.Message }

func IsRetryable(err error) bool {
	var target *RetryableError
	return errors.As(err, &target)
}

func RetryAfter(err error) time.Duration {
	var target *RetryableError
	if errors.As(err, &target) {
		return target.RetryAfter
	}
	return 0
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	for _, layout := range []string{http.TimeFormat, time.RFC1123, time.RFC1123Z} {
		if parsed, err := time.Parse(layout, value); err == nil {
			if wait := time.Until(parsed); wait > 0 {
				return wait
			}
			return 0
		}
	}
	return 0
}

func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type Profile struct {
	BaseURL     string
	Model       string
	VisionModel string
	APIFormat   string
	Key         string
	Parameters  map[string]config.ModelParameters
}

type Client struct {
	HTTP    *http.Client
	Profile Profile
}

func New(profile Profile) *Client {
	return &Client{HTTP: &http.Client{}, Profile: profile}
}

func errorHint(status int, detail string) string {
	if status == 404 {
		return "服务端未找到当前模型或接口，请在设置中重新选择文本/视觉模型，并确认 API 地址"
	}
	if status == 400 {
		switch {
		case regexp.MustCompile(`reasoning(?:_effort|\.effort)?|推理强度`).MatchString(detail):
			return "服务端拒绝了推理强度参数，请在模型参数中选择提供方默认"
		case regexp.MustCompile(`temperature|top_p`).MatchString(detail):
			return "服务端拒绝了温度或 Top P 参数，请恢复提供方默认"
		case regexp.MustCompile(`max_(?:output_)?tokens`).MatchString(detail):
			return "服务端拒绝了最大输出参数，请检查模型允许的输出上限"
		case regexp.MustCompile(`file|pptx?|attachment|mime|文件|附件`).MatchString(detail):
			return "服务端拒绝了课件文件输入，请核对该模型与接口允许的文件格式及大小"
		case regexp.MustCompile(`model|模型`).MatchString(detail):
			return "服务端拒绝了当前模型，请核对模型名与接口是否匹配"
		}
	}
	return "请检查地址、接口格式、模型、密钥或配额"
}

func requestError(response *http.Response, nativeFile, hasFile bool) error {
	hint := ""
	if response.StatusCode == 404 || response.StatusCode == 400 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Param   string `json:"param"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &payload)
		hint = errorHint(response.StatusCode, payload.Error.Code+" "+payload.Error.Param+" "+payload.Error.Message)
	}
	extra := ""
	if nativeFile {
		extra = "；PPT 原文件上传需要服务商支持 Responses 文件输入"
	} else if hasFile {
		extra = "；此任务需要服务商与模型支持 PDF 文件输入"
	}
	message := fmt.Sprintf("API 请求失败（HTTP %d），%s%s", response.StatusCode, hint, extra)
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return &RetryableError{Message: message, RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}
	}
	return errors.New(message)
}

func chatContentFlags(messages []Message) (bool, bool, bool) {
	nativeFile := false
	hasFile := false
	hasImages := false
	for _, message := range messages {
		parts, ok := message.Content.([]ContentPart)
		if !ok {
			continue
		}
		for _, part := range parts {
			switch part.Type {
			case "file":
				hasFile = true
				if part.File != nil && !strings.HasSuffix(strings.ToLower(part.File.Filename), ".pdf") {
					nativeFile = true
				}
			case "image_url":
				hasImages = true
			}
		}
	}
	return nativeFile, hasFile, hasImages
}

func chatModel(profile Profile, hasImages bool) (string, error) {
	model := profile.Model
	if hasImages {
		model = strings.TrimSpace(profile.VisionModel)
	}
	if strings.TrimSpace(model) != "" {
		return model, nil
	}
	if hasImages {
		return "", fmt.Errorf("请先在设置中选择千问视觉模型")
	}
	return "", fmt.Errorf("请先在设置中选择千问文本模型")
}

func chatEndpoint(profile Profile, responses bool) string {
	endpoint := strings.TrimRight(profile.BaseURL, "/")
	if responses {
		return endpoint + "/responses"
	}
	return endpoint + "/chat/completions"
}

func responsesInput(messages []Message) []map[string]any {
	converted := []map[string]any{}
	for _, message := range messages {
		content, ok := message.Content.([]ContentPart)
		if !ok {
			converted = append(converted, map[string]any{"role": message.Role, "content": message.Content})
			continue
		}
		parts := []map[string]any{}
		for _, part := range content {
			switch part.Type {
			case "text":
				parts = append(parts, map[string]any{"type": "input_text", "text": part.Text})
			case "image_url":
				url := ""
				if part.ImageURL != nil {
					url = part.ImageURL.URL
				}
				parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
			case "file":
				if part.File != nil {
					parts = append(parts, map[string]any{
						"type": "input_file", "filename": part.File.Filename, "file_data": part.File.FileData,
					})
				}
			}
		}
		converted = append(converted, map[string]any{"role": message.Role, "content": parts})
	}
	return converted
}

func chatBody(profile Profile, model string, input map[string]any, responses bool, stream bool) map[string]any {
	body := map[string]any{"model": model, "stream": stream}
	for key, value := range input {
		body[key] = value
	}
	parameters := profile.Parameters[model]
	if parameters.Temperature != nil {
		body["temperature"] = *parameters.Temperature
	}
	if parameters.TopP != nil {
		body["top_p"] = *parameters.TopP
	}
	if parameters.MaxTokens != nil {
		if responses {
			body["max_output_tokens"] = *parameters.MaxTokens
		} else {
			body["max_tokens"] = *parameters.MaxTokens
		}
	}
	if parameters.ReasoningEffort != nil {
		if responses {
			body["reasoning"] = map[string]any{"effort": *parameters.ReasoningEffort}
		} else {
			body["reasoning_effort"] = *parameters.ReasoningEffort
		}
	}
	return body
}

type chatPayload struct {
	Status            string          `json:"status"`
	IncompleteDetails json.RawMessage `json:"incomplete_details"`
	Output            []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func responsesOutput(payload chatPayload) string {
	parts := []string{}
	for _, item := range payload.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				parts = append(parts, part.Text)
			}
		}
	}
	return strings.Join(parts, "")
}

func choiceContent(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	texts := []string{}
	for _, part := range parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "")
}

func decodeChatResponse(response *http.Response, responses bool) (string, error) {
	var payload chatPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("API 响应格式异常，请稍后重试")
	}
	if payload.Status == "incomplete" || payload.Status == "failed" || (responses && payload.Status != "completed") {
		return "", fmt.Errorf("API 未完成输出，请检查输出长度限制或服务状态")
	}
	if !responses && len(payload.Choices) > 0 {
		switch payload.Choices[0].FinishReason {
		case "", "stop":
		case "length":
			return "", fmt.Errorf("API 输出达到长度限制，未保存不完整结果")
		case "content_filter":
			return "", fmt.Errorf("API 输出被内容安全过滤，未保存不完整结果")
		default:
			return "", fmt.Errorf("API 未正常结束，未保存结果")
		}
	}
	value := ""
	if responses {
		value = responsesOutput(payload)
	} else if len(payload.Choices) > 0 {
		value = choiceContent(payload.Choices[0].Message.Content)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("API 未返回有效文本")
	}
	return value, nil
}

func (client *Client) Chat(ctx context.Context, messages []Message, onText func(string)) (string, error) {
	profile := client.Profile
	nativeFile, hasFile, hasImages := chatContentFlags(messages)
	model, err := chatModel(profile, hasImages)
	if err != nil {
		return "", err
	}
	responses := nativeFile || profile.APIFormat == "responses"
	endpoint := chatEndpoint(profile, responses)
	var input map[string]any
	if responses {
		input = map[string]any{"input": responsesInput(messages), "store": false}
	} else {
		input = map[string]any{"messages": messages}
	}
	body := chatBody(profile, model, input, responses, onText != nil)
	encoded, err := marshalNoEscape(body)
	if err != nil {
		return "", err
	}
	requestContext, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	if profile.Key != "" {
		request.Header.Set("Authorization", "Bearer "+profile.Key)
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("任务已取消")
		}
		return "", &RetryableError{Message: "API 连接失败，请检查网络或服务地址"}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", requestError(response, nativeFile, hasFile)
	}
	if onText != nil && response.Body != nil {
		return readSSE(response.Body, onText)
	}
	return decodeChatResponse(response, responses)
}

func (client *Client) Models(ctx context.Context) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(client.Profile.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if client.Profile.Key != "" {
		request.Header.Set("Authorization", "Bearer "+client.Profile.Key)
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("读取模型失败，请检查地址与密钥")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("读取模型失败（HTTP %d），请检查地址与密钥；也可手动填写完整模型名", response.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("读取模型失败，请检查地址与密钥")
	}
	seen := map[string]bool{}
	models := []string{}
	for _, row := range payload.Data {
		if row.ID != "" && !seen[row.ID] {
			seen[row.ID] = true
			models = append(models, row.ID)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("服务未返回模型列表，请手动填写完整模型名")
	}
	return models, nil
}

type sseError struct{ message string }

func (value sseError) Error() string { return value.message }

type sseState struct {
	all          strings.Builder
	finishReason string
	completed    bool
	onText       func(string)
}

func sseBlockData(value string) string {
	dataLines := []string{}
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimLeft(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return strings.TrimSpace(strings.Join(dataLines, "\n"))
}

func (state *sseState) handleTerminal(payload map[string]any) error {
	if payload["error"] != nil || payload["type"] == "response.failed" || payload["type"] == "response.incomplete" {
		return sseError{message: "API 未完成流式输出，请检查服务配置或输出长度限制"}
	}
	if payload["type"] != "response.completed" {
		return nil
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if status, ok := response["status"].(string); ok && status != "completed" {
			return sseError{message: "API 未完成流式输出"}
		}
	}
	state.completed = true
	return nil
}

func (state *sseState) handleChoice(payload map[string]any) error {
	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil
	}
	reason, ok := choice["finish_reason"].(string)
	if !ok {
		return nil
	}
	state.finishReason = reason
	switch reason {
	case "length":
		return sseError{message: "API 输出达到长度限制，未保存不完整结果"}
	case "content_filter":
		return sseError{message: "API 输出被内容安全过滤，未保存不完整结果"}
	case "stop":
		state.completed = true
	case "":
	default:
		return sseError{message: "API 未正常结束文本输出，未保存结果"}
	}
	return nil
}

func sseDelta(payload map[string]any) string {
	if payload["type"] == "response.output_text.delta" {
		if value, ok := payload["delta"].(string); ok {
			return value
		}
		return ""
	}
	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	item, _ := choice["delta"].(map[string]any)
	value, _ := item["content"].(string)
	return value
}

func (state *sseState) handle(value string) error {
	data := sseBlockData(value)
	if data == "" {
		return nil
	}
	if data == "[DONE]" {
		if state.finishReason == "" || state.finishReason == "stop" {
			state.completed = true
		}
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return sseError{message: "API 流式响应格式异常"}
	}
	if err := state.handleTerminal(payload); err != nil {
		return err
	}
	if payload["type"] == "response.completed" {
		return nil
	}
	if err := state.handleChoice(payload); err != nil {
		return err
	}
	if delta := sseDelta(payload); delta != "" {
		state.all.WriteString(delta)
		state.onText(delta)
	}
	return nil
}

func readSSE(body io.Reader, onText func(string)) (string, error) {
	reader := bufio.NewReaderSize(body, 64*1024)
	buffer := ""
	state := &sseState{onText: onText}
	for {
		line, err := reader.ReadString('\n')
		buffer += strings.ReplaceAll(line, "\r", "")
		for {
			index := strings.Index(buffer, "\n\n")
			if index < 0 {
				break
			}
			block := buffer[:index]
			buffer = buffer[index+2:]
			if handleErr := state.handle(block); handleErr != nil {
				return "", handleErr
			}
		}
		if err != nil {
			if err == io.EOF {
				if strings.TrimSpace(buffer) != "" {
					if handleErr := state.handle(buffer); handleErr != nil {
						return "", handleErr
					}
				}
				break
			}
			return "", err
		}
	}
	if !state.completed {
		return "", fmt.Errorf("API 流提前结束，缺少有效完成标记，未保存不完整结果")
	}
	if strings.TrimSpace(state.all.String()) == "" {
		return "", fmt.Errorf("API 未返回文本")
	}
	return state.all.String(), nil
}

func stripJSONFence(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = regexp.MustCompile("(?is)^\\s*```(?:json)?\\s*").ReplaceAllString(trimmed, "")
	trimmed = regexp.MustCompile("(?is)\\s*```\\s*$").ReplaceAllString(trimmed, "")
	return strings.TrimSpace(trimmed)
}

type JSONOptions struct {
	Images  []Image
	OnRetry func()
}

func (client *Client) JSON(ctx context.Context, prompt string, input any, validate func([]byte) error) ([]byte, error) {
	return client.JSONWithOptions(ctx, prompt, input, validate, JSONOptions{})
}

func (client *Client) JSONWithOptions(
	ctx context.Context,
	prompt string,
	input any,
	validate func([]byte) error,
	options JSONOptions,
) ([]byte, error) {
	encoded, err := marshalNoEscape(input)
	if err != nil {
		return nil, err
	}
	feedback := ""
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		text := string(encoded) + feedback
		var content any = text
		if len(options.Images) > 0 {
			parts := []ContentPart{{Type: "text", Text: text}}
			for _, image := range options.Images {
				parts = append(parts, ContentPart{Type: "text", Text: image.Label})
				parts = append(parts, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: image.URL}})
			}
			content = parts
		}
		reply, err := client.Chat(ctx, []Message{
			{Role: "system", Content: prompt},
			{Role: "user", Content: content},
		}, nil)
		if err != nil {
			return nil, err
		}
		clean := []byte(stripJSONFence(reply))
		if err := validate(clean); err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == 1 {
				return nil, err
			}
			feedback = "\n上次输出未通过格式校验。请严格返回指定结构的合法 JSON，不加说明。"
			if options.OnRetry != nil {
				options.OnRetry()
			}
			timer := time.NewTimer(1500 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
			continue
		}
		return clean, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("结构化输出失败")
	}
	return nil, lastErr
}
