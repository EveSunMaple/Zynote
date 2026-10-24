package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAnswerChoicesAndOptions(t *testing.T) {
	if options := QuestionOptions("1. 题\nA. 甲\nB. 乙\nC. 丙"); len(options) != 3 || options[2] != "C" {
		t.Fatalf("options mismatch: %+v", options)
	}
	if options := QuestionOptions("A. 甲\nA. 乙"); len(options) != 0 {
		t.Fatalf("duplicate options must fail: %+v", options)
	}
	if keys := AnswerChoices("答案：C、A", []string{"A", "B", "C"}); len(keys) != 2 || keys[0] != "A" || keys[1] != "C" {
		t.Fatalf("answer choices mismatch: %+v", keys)
	}
	if keys := AnswerChoices("D", []string{"A", "B"}); len(keys) != 0 {
		t.Fatalf("unknown choice must fail: %+v", keys)
	}
	if !CompareChoices("C, A", []string{"A", "C"}) || CompareChoices("A", []string{"A", "C"}) {
		t.Fatal("compare choices mismatch")
	}
}

func TestPracticeKeywordCandidates(t *testing.T) {
	questions := []any{
		map[string]any{"stem": "机器学习概述", "knowledgePoints": []any{"特征工程"}},
		map[string]any{"stem": "数据清洗", "knowledgePoints": []any{"缺失值"}},
	}
	if result := PracticeKeywordCandidates(questions, []string{"特征"}); len(result) != 1 || result[0] != 0 {
		t.Fatalf("candidates mismatch: %+v", result)
	}
	if result := PracticeKeywordCandidates(questions, []string{"清洗"}); len(result) != 1 || result[0] != 1 {
		t.Fatalf("stem candidates mismatch: %+v", result)
	}
}

func fakeExplanationJSON(t *testing.T) func(context.Context, string, any, func([]byte) error) ([]byte, error) {
	return func(_ context.Context, system string, _ any, validate func([]byte) error) ([]byte, error) {
		var payload string
		switch {
		case strings.Contains(system, "寻找与题干完全同题"):
			payload = `{"sourceUrl":"","quote":"","answer":"","explanation":""}`
		case strings.Contains(system, "你是课程练习助手"):
			payload = `{"answer":"AI 答案","explanation":"AI 详解"}`
		default:
			return nil, fmt.Errorf("unexpected prompt: %s", system)
		}
		raw := []byte(payload)
		if err := validate(raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
}

func TestPrepareAnswerImported(t *testing.T) {
	question := map[string]any{
		"stem": "1. 机器学习的主要目标是什么\nA. 特征工程\nB. 数据清洗", "answer": "A",
		"explanation": "解释", "type": "单选",
	}
	called := false
	result, err := PrepareAnswer(context.Background(), question, "课程", PracticeDependencies{
		Search: func(context.Context, string) ([]PracticeSource, error) {
			called = true
			return nil, nil
		},
		JSON: func(context.Context, string, any, func([]byte) error) ([]byte, error) {
			called = true
			return nil, fmt.Errorf("must not call")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called || result["answerSource"] != "imported" || result["aiAnswer"] != false {
		t.Fatalf("imported answer mismatch: %+v", result)
	}
	keys, _ := result["correctOptionKeys"].([]string)
	if len(keys) != 1 || keys[0] != "A" {
		t.Fatalf("correct keys mismatch: %+v", keys)
	}
}

func TestPrepareAnswerVerifiedSearch(t *testing.T) {
	quote := "机器学习的主要目标是什么？答案：特征工程"
	question := map[string]any{"stem": "机器学习的主要目标是什么？", "type": "简答"}
	result, err := PrepareAnswer(context.Background(), question, "课程", PracticeDependencies{
		Search: func(context.Context, string) ([]PracticeSource, error) {
			return []PracticeSource{{URL: "https://example.com/a", Text: "原题：" + quote}}, nil
		},
		JSON: func(_ context.Context, system string, _ any, validate func([]byte) error) ([]byte, error) {
			if !strings.Contains(system, "寻找与题干完全同题") {
				return nil, fmt.Errorf("unexpected prompt")
			}
			payload, _ := json.Marshal(map[string]any{
				"sourceUrl": "https://example.com/a", "quote": quote,
				"answer": "特征工程", "explanation": "来源于教材",
			})
			if err := validate(payload); err != nil {
				return nil, err
			}
			return payload, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["answerSource"] != "searched" || result["answer"] != "特征工程" ||
		result["answerSourceUrl"] != "https://example.com/a" || result["aiAnswer"] != false ||
		result["searchStatus"] != "已核验搜索来源中的同题答案" {
		t.Fatalf("verified search mismatch: %+v", result)
	}
}

func TestPrepareAnswerSearchFailureFallsBackToAI(t *testing.T) {
	result, err := PrepareAnswer(
		context.Background(),
		map[string]any{"stem": "机器学习的主要目标是什么？", "type": "简答"},
		"课程",
		PracticeDependencies{
			Search: func(context.Context, string) ([]PracticeSource, error) {
				return nil, fmt.Errorf("offline")
			},
			JSON: fakeExplanationJSON(t),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result["searchStatus"] != "搜索暂不可用，使用 AI 参考答案" || result["answerSource"] != "ai" ||
		result["answer"] != "AI 答案" || result["aiAnswer"] != true {
		t.Fatalf("fallback mismatch: %+v", result)
	}
}

func TestPrepareAnswerUnverifiedLookupFallsBack(t *testing.T) {
	question := map[string]any{"stem": "机器学习的主要目标是什么？", "type": "简答"}
	result, err := PrepareAnswer(context.Background(), question, "课程", PracticeDependencies{
		Search: func(context.Context, string) ([]PracticeSource, error) {
			return []PracticeSource{{URL: "https://example.com/a", Text: "无关内容"}}, nil
		},
		JSON: fakeExplanationJSON(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["searchStatus"] != "未找到可核验的同题答案" || result["answerSource"] != "ai" ||
		result["aiAnswer"] != true {
		t.Fatalf("unverified mismatch: %+v", result)
	}
}

func TestPrepareAnswerFillsMissingExplanation(t *testing.T) {
	result, err := PrepareAnswer(
		context.Background(),
		map[string]any{"stem": "1. 题\nA. 甲\nB. 乙", "answer": "A", "type": "单选"},
		"课程",
		PracticeDependencies{JSON: fakeExplanationJSON(t)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result["answerSource"] != "imported" || result["answer"] != "A" ||
		result["explanation"] != "AI 详解" || result["aiAnswer"] != false {
		t.Fatalf("missing explanation mismatch: %+v", result)
	}
}
