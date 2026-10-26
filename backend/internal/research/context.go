package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ContextRow struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Version string `json:"version"`
}

type Context struct {
	Channels     []string         `json:"channels"`
	Conversation *ConversationRef `json:"conversation,omitempty"`
	ExcludedTask string           `json:"excludedTask,omitempty"`
	Rows         []ContextRow     `json:"rows"`
	Digest       string           `json:"digest"`
}

type ConversationRef struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

func (store *Store) ContextSnapshot(channels []string, question string, conversation *ConversationRef, excludedTask string) (Context, error) {
	terms := contextTerms(question)
	score := func(value string) int {
		lower := strings.ToLower(value)
		total := 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				total++
			}
		}
		return total
	}
	rows := []ContextRow{}
	if contains(channels, "practice") {
		questions, err := store.PracticeQuestions(question, "")
		if err != nil {
			return Context{}, err
		}
		for _, raw := range questions {
			encoded, _ := json.Marshal(raw)
			rows = append(rows, ContextRow{
				Kind: "practice", ID: text(raw["question_id"]), Title: "练习题干（未读取答案、作答或成绩）",
				Text: text(raw["question"]), Version: hashText(string(encoded)),
			})
		}
	}
	if contains(channels, "memory") {
		memories, err := store.Memories()
		if err != nil {
			return Context{}, err
		}
		for _, raw := range memories["records"].([]map[string]any) {
			if text(raw["availability"]) != "active" {
				continue
			}
			version, _ := json.Marshal(raw)
			rows = append(rows, ContextRow{
				Kind: "memory", ID: text(raw["memory_id"]), Title: text(raw["title"]),
				Text: text(raw["body"]), Version: hashText(string(version)),
			})
		}
	}
	if contains(channels, "notes") {
		noteRows, err := store.List("note")
		if err != nil {
			return Context{}, err
		}
		for _, raw := range noteRows {
			value, err := parseNote(raw)
			if err != nil || value.Lifecycle.State != "active" {
				continue
			}
			if hashText(value.Markdown) != value.Receipt.BodySHA256 {
				return Context{}, fmt.Errorf("已保存笔记校验失败，未发送研究")
			}
			version, _ := json.Marshal(raw)
			markdown := []rune(value.Markdown)
			if len(markdown) > 12000 {
				markdown = markdown[:12000]
			}
			rows = append(rows, ContextRow{
				Kind: "note", ID: value.Receipt.NoteID, Title: value.Receipt.Title,
				Text: string(markdown), Version: hashText(string(version)),
			})
		}
	}
	if contains(channels, "conversations") && conversation != nil {
		current, err := store.Conversation(conversation.ID)
		if err != nil {
			return Context{}, err
		}
		if int(number(current["revision"])) != conversation.Revision {
			return Context{}, fmt.Errorf("所选知识会话版本已改变")
		}
		if tasks, ok := current["research_tasks"].([]any); ok {
			for _, raw := range tasks {
				task, _ := raw.(map[string]any)
				if task == nil || text(task["task_id"]) == excludedTask || text(task["status"]) != "completed" {
					continue
				}
				note := text(task["note"])
				if note == "" {
					continue
				}
				if hash := text(task["answerHash"]); hash != "" && hash != hashText(note) {
					return Context{}, fmt.Errorf("会话回答与保存回执不符")
				}
				version, _ := json.Marshal(task)
				textValue := []rune(note)
				if len(textValue) > 12000 {
					textValue = textValue[:12000]
				}
				rows = append(rows, ContextRow{
					Kind: "conversation", ID: text(task["task_id"]), Title: text(task["question"]),
					Text: string(textValue), Version: hashText(string(version)),
				})
			}
		}
		if turns, ok := current["turns"].([]any); ok {
			for index, raw := range turns {
				turn, _ := raw.(map[string]any)
				if turn == nil {
					continue
				}
				answer := text(turn["answer"])
				if answer == "" {
					answer = text(turn["text"])
				}
				if answer == "" {
					continue
				}
				version, _ := json.Marshal(turn)
				turnText := []rune(answer)
				if len(turnText) > 12000 {
					turnText = turnText[:12000]
				}
				rows = append(rows, ContextRow{
					Kind: "conversation", ID: "turn-" + text(turn["turn_id"]) + itoa(index),
					Title: text(current["title"]), Text: string(turnText), Version: hashText(string(version)),
				})
			}
		}
	}
	sort.SliceStable(rows, func(left, right int) bool {
		leftScore := score(rows[left].Title + " " + rows[left].Text)
		rightScore := score(rows[right].Title + " " + rows[right].Text)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if rows[left].Kind != rows[right].Kind {
			return rows[left].Kind < rows[right].Kind
		}
		return rows[left].ID < rows[right].ID
	})
	if len(rows) > 12 {
		rows = rows[:12]
	}
	budget := 24000
	finalRows := []ContextRow{}
	for _, row := range rows {
		if budget <= 0 {
			break
		}
		textRunes := []rune(row.Text)
		limit := 6000
		if budget < limit {
			limit = budget
		}
		if len(textRunes) > limit {
			textRunes = textRunes[:limit]
		}
		row.Text = string(textRunes)
		budget -= len(textRunes)
		finalRows = append(finalRows, row)
	}
	encoded, _ := json.Marshal(finalRows)
	sum := sha256.Sum256(encoded)
	return Context{
		Channels: channels, Conversation: conversation, ExcludedTask: excludedTask,
		Rows: finalRows, Digest: hex.EncodeToString(sum[:]),
	}, nil
}

func (store *Store) VerifyContext(snapshot Context, _ string) error {
	encoded, _ := json.Marshal(snapshot.Rows)
	sum := sha256.Sum256(encoded)
	if hex.EncodeToString(sum[:]) != snapshot.Digest {
		return fmt.Errorf("研究上下文回执无效")
	}
	if snapshot.Conversation != nil {
		current, err := store.Conversation(snapshot.Conversation.ID)
		if err != nil || int(number(current["revision"])) != snapshot.Conversation.Revision {
			return fmt.Errorf("所选知识会话版本已改变")
		}
	}
	for _, row := range snapshot.Rows {
		var current any
		switch row.Kind {
		case "memory":
			memories, err := store.Memories()
			if err != nil {
				return err
			}
			for _, item := range memories["records"].([]map[string]any) {
				if text(item["memory_id"]) == row.ID && text(item["availability"]) == "active" {
					current = item
					break
				}
			}
		case "note":
			if value, err := store.Read("note", row.ID); err == nil {
				current = value
			}
		case "conversation":
			// Conversation rows are verified by revision above; individual turn changes are checked by digest at creation.
			continue
		}
		if current == nil {
			return fmt.Errorf("研究期间记忆、笔记或历史问答已改变，请重新研究")
		}
		version, _ := json.Marshal(current)
		if hashText(string(version)) != row.Version {
			return fmt.Errorf("研究期间记忆、笔记或历史问答已改变，请重新研究")
		}
	}
	return nil
}

func contextTerms(question string) []string {
	lower := strings.ToLower(question)
	fields := strings.FieldsFunc(lower, func(char rune) bool {
		return !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || isHan(char))
	})
	terms := []string{}
	for _, field := range fields {
		runes := []rune(field)
		if len(runes) == 0 {
			continue
		}
		if isHan(runes[0]) {
			for size := 1; size <= 2 && size <= len(runes); size++ {
				for index := 0; index+size <= len(runes); index++ {
					terms = append(terms, string(runes[index:index+size]))
				}
			}
		} else {
			terms = append(terms, field)
		}
	}
	return terms
}

func isHan(char rune) bool {
	return (char >= 0x3400 && char <= 0x4DBF) || (char >= 0x4E00 && char <= 0x9FFF) ||
		(char >= 0xF900 && char <= 0xFAFF) || (char >= 0x20000 && char <= 0x2FA1F)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
