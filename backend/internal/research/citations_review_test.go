package research

import (
	"fmt"
	"strings"
	"testing"
)

// 以下用例对应维护者审查 PR #5 时指出的空缺。

func spacedSRT(step int, lines ...string) string {
	blocks := []string{}
	for index, line := range lines {
		start := index * step
		blocks = append(blocks, fmt.Sprintf("%d\n%s,000 --> %s,500\n%s", index+1, clockText(start), clockText(start+step-1), line))
	}
	return strings.Join(blocks, "\n\n")
}

func onlySource(text string) []TaskSource {
	return []TaskSource{{SourceID: "S1", Text: sourceView(text), Read: true}}
}

func TestMixedSubtitleKeepsProse(t *testing.T) {
	text := "讲义：泰勒公式用多项式逼近函数\n\n" + sampleSRT("今天讲泰勒公式", "余项有两种形式", "先看佩亚诺余项") +
		"\n\n课后说明：拉格朗日余项用于估计误差"
	view := sourceView(text)
	for _, want := range []string{"讲义：泰勒公式用多项式逼近函数", "余项有两种形式", "课后说明：拉格朗日余项用于估计误差"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lost %q:\n%s", want, view)
		}
	}
	checks := CheckQuotes("讲义写道“拉格朗日余项用于估计误差”[S1]。", onlySource(text))
	if len(checks) != 1 || checks[0].Status != "exact" {
		t.Fatalf("prose quote should be found: %+v", checks)
	}
}

func TestLongQuoteWithRightStartIsNotMoved(t *testing.T) {
	// 每条字幕相隔 60 秒，引文跨 4 条（3 分钟），起点就是所注时间。
	text := spacedSRT(60, "开场白", "第一句讲泰勒公式的来历", "第二句讲多项式逼近", "第三句讲余项的意义", "第四句讲误差估计", "结束")
	note := "老师说“第一句讲泰勒公式的来历 第二句讲多项式逼近 第三句讲余项的意义 第四句讲误差估计”[S1@00:01:00]。"
	checks := CheckQuotes(note, onlySource(text))
	if len(checks) != 1 || checks[0].Status != "exact" || checks[0].At != "00:01:00" {
		t.Fatalf("long quote cited at its real start must be exact: %+v", checks)
	}
	far := strings.Replace(note, "00:01:00", "00:09:00", 1)
	if checks := CheckQuotes(far, onlySource(text)); len(checks) != 1 || checks[0].Status != "moved" {
		t.Fatalf("quote cited far from its real start must be moved: %+v", checks)
	}
}

func TestExactElsewhereBeatsNearbyApproximation(t *testing.T) {
	lines := []string{"佩亚诺余项求极限很好用的", "中间内容一", "中间内容二", "中间内容三", "中间内容四",
		"中间内容五", "中间内容六", "中间内容七", "中间内容八", "中间内容九", "中间内容十", "中间内容十一",
		"佩亚诺余项求极限很好用"}
	text := spacedSRT(10, lines...)
	// 所注时间 00:00:00 附近只有近似句（多一个“的”），00:02:00 处逐字一致。
	checks := CheckQuotes("“佩亚诺余项求极限很好用”[S1@00:00:00]", onlySource(text))
	if len(checks) != 1 || checks[0].Status == "edited" {
		t.Fatalf("a verbatim hit elsewhere must win over a nearby approximation: %+v", checks)
	}
}

func TestMultiSourceCitationPicksBestSource(t *testing.T) {
	filler := strings.Split(strings.TrimSuffix(strings.Repeat("其他内容,", 30), ","), ",")
	first := spacedSRT(10, append(filler, "余项有两种形式")...)
	second := spacedSRT(10, "开场", "余项有两种形式", "结束")
	sources := []TaskSource{
		{SourceID: "S1", Text: sourceView(first), Read: true},
		{SourceID: "S2", Text: sourceView(second), Read: true},
	}
	// S1 里这句在 00:05:00（离所注时间远，会是“时间不符”），S2 在所注时间逐字一致。
	checks := CheckQuotes("“余项有两种形式”[S1, S2@00:00:10]", sources)
	if len(checks) != 1 || checks[0].SourceID != "S2" || checks[0].Status != "exact" {
		t.Fatalf("multi-source citation must pick the exact source: %+v", checks)
	}
	// 第一个编号不可读时，后面的可读编号照样核对。
	unread := []TaskSource{{SourceID: "S1", Read: false}, sources[1]}
	checks = CheckQuotes("“余项有两种形式”[S1, S2@00:00:10]", unread)
	if len(checks) != 1 || checks[0].SourceID != "S2" || checks[0].Status != "exact" {
		t.Fatalf("an unreadable first id must not block the rest: %+v", checks)
	}
}

func TestAsciiQuotesAndFullwidthTime(t *testing.T) {
	text := sampleSRT("今天讲泰勒公式", "余项有两种形式", "先看佩亚诺余项")
	checks := CheckQuotes(`老师说"余项有两种形式"[S1@00：00：10]。`, onlySource(text))
	if len(checks) != 1 || checks[0].Status != "exact" || checks[0].CitedAt != "00:00:10" {
		t.Fatalf("ASCII quotes and full-width colons should be understood: %+v", checks)
	}
}

func TestOriginalSubtitleMarksTruncation(t *testing.T) {
	lines := []string{}
	for index := 0; index < 12; index++ {
		lines = append(lines, fmt.Sprintf("第%d句讲内容", index+1))
	}
	text := sampleSRT(lines...)
	checks := CheckQuotes("“"+strings.Join(lines, "")+"”[S1@00:00:00]", onlySource(text))
	if len(checks) != 1 || !strings.Contains(checks[0].Original, "……") {
		t.Fatalf("a truncated original subtitle must say so: %+v", checks)
	}
}

func TestQuoteChecksAreCapped(t *testing.T) {
	text := sampleSRT("今天讲泰勒公式", "余项有两种形式", "先看佩亚诺余项")
	note := strings.Repeat("“余项有两种形式”[S1@00:00:10]\n", maxQuoteChecks+50)
	if checks := CheckQuotes(note, onlySource(text)); len(checks) != maxQuoteChecks {
		t.Fatalf("quote checks should stop at %d, got %d", maxQuoteChecks, len(checks))
	}
}
