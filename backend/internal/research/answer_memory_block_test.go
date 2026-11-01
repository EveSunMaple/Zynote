package research

import (
	"strings"
	"testing"
)

const memoryQuestion = "我总是记不住 ln(1+x) 展开式的符号，复习时请先提醒我。"

func TestMemoryBlockParsed(t *testing.T) {
	raw := "老师讲的展开式是“X减二分之X的平方”[S1@00:35:28]。\n\n```memory\n" +
		`[{"kind":"preference","title":"先查符号","body":"复习 ln(1+x) 时先提醒检查符号。","basis":"question","quote":"我总是记不住 ln(1+x) 展开式的符号"},` +
		`{"kind":"preference","title":"编造的原句","body":"不合格","basis":"question","quote":"问题里没有这句话"}]` + "\n```\n"
	note, candidates, err := ReadResearchAnswer(raw, "t1", memoryQuestion, "范围", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "memory") || !strings.HasSuffix(note, "[S1@00:35:28]。") {
		t.Fatalf("memory block must be stripped from note: %q", note)
	}
	if len(candidates) != 1 || candidates[0].Title != "先查符号" || candidates[0].Status != "pending" {
		t.Fatalf("only the verbatim-quoted candidate must be kept: %+v", candidates)
	}
}

// 代码块写坏只丢建议，回答照常保留。
func TestBrokenMemoryBlockKeepsNote(t *testing.T) {
	raw := "回答正文[S1]\n```memory\n[{坏掉的 JSON\n```"
	note, candidates, err := ReadResearchAnswer(raw, "t1", memoryQuestion, "", true)
	if err != nil || note != "回答正文[S1]" || len(candidates) != 0 {
		t.Fatalf("got %q %+v %v", note, candidates, err)
	}
}

// 以普通代码块开头的 Markdown 回答不能被当成坏 JSON 而整次作废。
func TestMarkdownStartingWithCodeFence(t *testing.T) {
	raw := "```\nf(x) = x - x^2/2\n```\n上式见[S1]"
	note, _, err := ReadResearchAnswer(raw, "t1", memoryQuestion, "", true)
	if err != nil || note != raw {
		t.Fatalf("got %q %v", note, err)
	}
}

// 未勾选学习记忆时，即使回答里有 memory 代码块也原样作为正文，不产生建议。
func TestMemoryBlockIgnoredWithoutMemoryChannel(t *testing.T) {
	raw := "正文[S1]\n```memory\n[]\n```"
	note, candidates, err := ReadResearchAnswer(raw, "t1", memoryQuestion, "", false)
	if err != nil || note != raw || len(candidates) != 0 {
		t.Fatalf("got %q %+v %v", note, candidates, err)
	}
}
