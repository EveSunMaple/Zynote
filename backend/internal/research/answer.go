package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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

func memoryCandidate(value struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Basis string `json:"basis"`
	Quote string `json:"quote"`
}, basis map[string]string, taskID string, index int) (MemoryCandidate, error) {
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

func ReadResearchAnswer(raw string, taskID, question, limitations string, allowSuggestions bool) (string, []MemoryCandidate, error) {
	note := raw
	candidates := []MemoryCandidate{}
	trimmed := strings.TrimSpace(raw)
	if allowSuggestions && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "```json") || strings.HasPrefix(trimmed, "```")) {
		clean := trimmed
		if strings.HasPrefix(clean, "```") {
			if index := strings.Index(clean, "\n"); index >= 0 {
				clean = clean[index+1:]
			}
			clean = strings.TrimSuffix(strings.TrimSpace(clean), "```")
		}
		var payload struct {
			Note             string `json:"note"`
			MemoryCandidates []struct {
				Kind  string `json:"kind"`
				Title string `json:"title"`
				Body  string `json:"body"`
				Basis string `json:"basis"`
				Quote string `json:"quote"`
			} `json:"memory_candidates"`
		}
		if json.Unmarshal([]byte(clean), &payload) != nil || payload.Note == "" || len(payload.Note) > 200000 ||
			len(payload.MemoryCandidates) > 3 {
			return "", nil, fmt.Errorf("研究回答格式无效")
		}
		note = payload.Note
		basis := map[string]string{"question": question, "note": note, "limitations": limitations}
		for index, value := range payload.MemoryCandidates {
			candidate, err := memoryCandidate(value, basis, taskID, index)
			if err != nil {
				return "", nil, err
			}
			candidates = append(candidates, candidate)
		}
	}
	return note, candidates, nil
}
