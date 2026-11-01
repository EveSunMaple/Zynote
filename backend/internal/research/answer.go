package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
)

type MemoryCandidate struct {
	CandidateID string `json:"candidate_id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Basis       string `json:"basis"`
	Quote       string `json:"quote"`
	Status      string `json:"status"`
}

// memoryCandidateInput 是模型给出的一条记忆建议，保存前逐项校验。
type memoryCandidateInput struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Basis string `json:"basis"`
	Quote string `json:"quote"`
}

func memoryCandidate(value memoryCandidateInput, basis map[string]string, taskID string, index int) (MemoryCandidate, error) {
	original := basis[value.Basis]
	if original == "" || value.Quote == "" || !strings.Contains(original, value.Quote) ||
		(value.Kind == "preference" && value.Basis != "question") ||
		(value.Kind != "preference" && value.Kind != "progress" && value.Kind != "correction") ||
		strings.TrimSpace(value.Title) == "" || len([]rune(value.Title)) > 80 ||
		strings.TrimSpace(value.Body) == "" || len([]rune(value.Body)) > 1000 {
		return MemoryCandidate{}, fmt.Errorf("记忆建议未包含相应字段原句，未保存建议或研究成果")
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%q", []any{taskID, index, value})))
	return MemoryCandidate{
		CandidateID: hex.EncodeToString(hash[:])[:32], Kind: value.Kind, Title: strings.TrimSpace(value.Title),
		Body: strings.TrimSpace(value.Body), Basis: value.Basis, Quote: value.Quote, Status: "pending",
	}, nil
}

// escapeRawControls 把 JSON 字符串里未转义的换行、回车、制表符改成转义形式。
func escapeRawControls(data []byte) []byte {
	out := make([]byte, 0, len(data)+16)
	inString, escaped := false, false
	for _, char := range data {
		switch {
		case escaped:
			escaped = false
		case inString && char == '\\':
			escaped = true
		case char == '"':
			inString = !inString
		case inString && char == '\n':
			out = append(out, '\\', 'n')
			continue
		case inString && char == '\r':
			out = append(out, '\\', 'r')
			continue
		case inString && char == '\t':
			out = append(out, '\\', 't')
			continue
		}
		out = append(out, char)
	}
	return out
}

// decodeLenient 先按标准 JSON 解析；失败时修复 LaTeX 反斜杠和字符串里的原始换行后再试。
func decodeLenient(data []byte, target any) error {
	if json.Unmarshal(data, target) == nil {
		return nil
	}
	return json.Unmarshal(escapeRawControls(ai.RepairJSONEscapes(data)), target)
}

// acceptCandidates 只留下合格的建议：不合格的直接丢弃，不连累回答本身；最多留 3 条。
func acceptCandidates(values []memoryCandidateInput, basis map[string]string, taskID string) []MemoryCandidate {
	candidates := []MemoryCandidate{}
	for index, value := range values {
		if len(candidates) >= 3 {
			break
		}
		if candidate, err := memoryCandidate(value, basis, taskID, index); err == nil {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// memoryBlockPattern 匹配回答末尾的 ```memory 代码块（记忆建议）。
var memoryBlockPattern = regexp.MustCompile("(?s)```memory[ \\t]*\\r?\\n(.*?)```\\s*$")

// ReadResearchAnswer 从模型回答中取出正文和记忆建议。
// 现行格式：Markdown 正文，末尾可附 ```memory 代码块；也兼容整段 JSON {note, memory_candidates}。
func ReadResearchAnswer(raw string, taskID, question, limitations string, allowSuggestions bool) (string, []MemoryCandidate, error) {
	if !allowSuggestions {
		return raw, []MemoryCandidate{}, nil
	}
	if match := memoryBlockPattern.FindStringSubmatchIndex(raw); match != nil {
		note := strings.TrimSpace(raw[:match[0]])
		var values []memoryCandidateInput
		if decodeLenient([]byte(raw[match[2]:match[3]]), &values) != nil {
			return note, []MemoryCandidate{}, nil
		}
		basis := map[string]string{"question": question, "note": note, "limitations": limitations}
		return note, acceptCandidates(values, basis, taskID), nil
	}
	trimmed := strings.TrimSpace(raw)
	// 只有整段 JSON（或 ```json 包裹）才按旧格式解析；以普通代码块开头的 Markdown 回答照常当正文。
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "```json") {
		return raw, []MemoryCandidate{}, nil
	}
	clean := trimmed
	if strings.HasPrefix(clean, "```") {
		if index := strings.Index(clean, "\n"); index >= 0 {
			clean = clean[index+1:]
		}
		clean = strings.TrimSuffix(strings.TrimSpace(clean), "```")
	}
	var payload struct {
		Note             string                 `json:"note"`
		MemoryCandidates []memoryCandidateInput `json:"memory_candidates"`
	}
	// 模型常在 JSON 字符串里写 LaTeX（如 \xi）或直接换行，先修复再解析，避免整次回答作废。
	if decodeLenient([]byte(clean), &payload) != nil || payload.Note == "" || len(payload.Note) > 200000 {
		return "", nil, fmt.Errorf("研究回答格式无效")
	}
	basis := map[string]string{"question": question, "note": payload.Note, "limitations": limitations}
	return payload.Note, acceptCandidates(payload.MemoryCandidates, basis, taskID), nil
}
