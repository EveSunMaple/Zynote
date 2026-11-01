package research

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 字幕类资料以紧凑形式交给模型：每条字幕一行 "[hh:mm:ss]原话"，
// 去掉 SRT 的序号行和结束时间，整节课可放进上下文，引用时可精确到秒。

type cue struct {
	Start int // 秒；-1 表示无时间（普通文本）
	Text  string
}

var srtTimePattern = regexp.MustCompile(`^(\d{1,2}):(\d{2}):(\d{2})[,.]\d{1,3}\s*-->`)
var srtBlockPattern = regexp.MustCompile(`\r?\n\s*\r?\n`)
var compactLinePattern = regexp.MustCompile(`^\[(\d{2}):(\d{2}):(\d{2})\](.*)$`)

func parseSRT(text string) ([]cue, bool) {
	blocks := srtBlockPattern.Split(strings.TrimPrefix(text, "\uFEFF"), -1)
	cues := []cue{}
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		lines := strings.Split(strings.TrimSpace(block), "\n")
		timeLine := -1
		for index, line := range lines {
			if index > 1 {
				break
			}
			if srtTimePattern.MatchString(strings.TrimSpace(line)) {
				timeLine = index
				break
			}
		}
		// 只有整份资料都由字幕块组成时才压缩；混合笔记必须保留非时间块正文。
		if timeLine < 0 {
			return nil, false
		}
		match := srtTimePattern.FindStringSubmatch(strings.TrimSpace(lines[timeLine]))
		body := []string{}
		for _, line := range lines[timeLine+1:] {
			if value := strings.TrimSpace(line); value != "" {
				body = append(body, value)
			}
		}
		if len(body) == 0 {
			continue
		}
		cues = append(cues, cue{Start: clockSeconds(match[1], match[2], match[3]), Text: strings.Join(body, " ")})
	}
	return cues, len(cues) >= 3
}

func clockSeconds(hours, minutes, seconds string) int {
	h, _ := strconv.Atoi(hours)
	m, _ := strconv.Atoi(minutes)
	s, _ := strconv.Atoi(seconds)
	return h*3600 + m*60 + s
}

func clockText(seconds int) string {
	if seconds < 0 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// sourceView 返回交给模型阅读的资料文本：字幕转为紧凑行，其他资料保持原文。
func sourceView(text string) string {
	cues, ok := parseSRT(text)
	if !ok {
		return text
	}
	lines := make([]string, 0, len(cues))
	for _, item := range cues {
		lines = append(lines, "["+clockText(item.Start)+"]"+item.Text)
	}
	return strings.Join(lines, "\n")
}

// viewCues 把阅读文本切回可定位的片段：紧凑字幕按行带时间，其他文本按段落。
func viewCues(view string) []cue {
	cues := []cue{}
	for _, line := range strings.Split(view, "\n") {
		if match := compactLinePattern.FindStringSubmatch(line); match != nil {
			cues = append(cues, cue{Start: clockSeconds(match[1], match[2], match[3]), Text: match[4]})
		} else if strings.TrimSpace(line) != "" {
			cues = append(cues, cue{Start: -1, Text: strings.TrimSpace(line)})
		}
	}
	return cues
}

// coverageText 说明本次实际读取的范围，供界面和模型如实声明。
func coverageText(full, read string) string {
	fullCues := viewCues(full)
	readCues := viewCues(read)
	if len(fullCues) == 0 || fullCues[0].Start < 0 || len(readCues) == 0 {
		if len([]rune(read)) < len([]rune(full)) {
			return fmt.Sprintf("已读前 %d 字（全文 %d 字）", len([]rune(read)), len([]rune(full)))
		}
		return ""
	}
	last := readCues[len(readCues)-1].Start
	end := fullCues[len(fullCues)-1].Start
	if len([]rune(read)) < len([]rune(full)) {
		return fmt.Sprintf("只读到 %s（全长 %s），之后的内容未读取", clockText(last), clockText(end))
	}
	return fmt.Sprintf("已读整节 %s–%s", clockText(fullCues[0].Start), clockText(end))
}

// allocateBudget 按“水位线”公平分配上下文：短资料全给，余量平均分给长资料。
func allocateBudget(lengths []int, total int) []int {
	caps := make([]int, len(lengths))
	order := make([]int, len(lengths))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool { return lengths[order[left]] < lengths[order[right]] })
	remaining := total
	for position, index := range order {
		share := remaining / (len(order) - position)
		if lengths[index] <= share {
			caps[index] = lengths[index]
		} else {
			caps[index] = share
		}
		remaining -= caps[index]
	}
	return caps
}

// ---- 引用解析 ----

type citationRef struct {
	IDs   []string
	Time  int // 秒，-1 表示未注明
	Start int // 在回答中的 rune 位置
	End   int
}

var citationPattern = regexp.MustCompile(`[\[【［]\s*(S\d+[^\[\]【】［］\n]{0,48})[\]】］]`)
var citationIDPattern = regexp.MustCompile(`S(\d+)`)
var citationTimePattern = regexp.MustCompile(`(\d{1,2}):(\d{2})(?::(\d{2}))?`)

func parseTime(value string) int {
	match := citationTimePattern.FindStringSubmatch(strings.ReplaceAll(value, "：", ":"))
	if match == nil {
		return -1
	}
	if match[3] == "" {
		return clockSeconds("0", match[1], match[2])
	}
	return clockSeconds(match[1], match[2], match[3])
}

// runeOffsets 预计算每个字节位置对应的 rune 下标，避免每个引用都重扫前缀。
func runeOffsets(value string) []int {
	offsets := make([]int, len(value)+1)
	count := 0
	for index := 0; index < len(value); {
		offsets[index] = count
		_, size := utf8.DecodeRuneInString(value[index:])
		for offset := 1; offset < size; offset++ {
			offsets[index+offset] = count
		}
		index += size
		count++
	}
	offsets[len(value)] = count
	return offsets
}

func findCitations(note string) []citationRef {
	offsets := runeOffsets(note)
	refs := []citationRef{}
	for _, match := range citationPattern.FindAllStringSubmatchIndex(note, -1) {
		inner := note[match[2]:match[3]]
		ids := []string{}
		for _, id := range citationIDPattern.FindAllStringSubmatch(inner, -1) {
			ids = append(ids, "S"+id[1])
		}
		refs = append(refs, citationRef{IDs: ids, Time: parseTime(inner), Start: offsets[match[0]], End: offsets[match[1]]})
	}
	return refs
}

// validResearchCitations 接受 [S1]、[S1@00:14:06]、[S1, 14:06-14:22]、[S1, S2] 等写法，
// 只要求至少有一处引用、且编号都指向已读来源。
func validResearchCitations(note string, sources []TaskSource) bool {
	refs := findCitations(note)
	if len(sources) > 0 && len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		for _, id := range ref.IDs {
			found := false
			for _, source := range sources {
				if source.SourceID == id && source.Read {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// ---- 原话核对 ----

type QuoteCheck struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
	CitedAt  string `json:"cited_at,omitempty"`
	Status   string `json:"status"` // exact 逐字一致 | edited 有删改 | moved 原话属实但时间不符 | unverified 待核对
	At       string `json:"at,omitempty"`
	Original string `json:"original,omitempty"`
}

var quotePattern = regexp.MustCompile(`[“「]([^”」\n]{2,1000})[”」]`)
var asciiQuotePattern = regexp.MustCompile(`"([^"\n]{2,1000})"`)

type quoteMatch struct {
	Text  string
	Start int
	End   int
}

func findQuotes(note string) []quoteMatch {
	offsets := runeOffsets(note)
	matches := []quoteMatch{}
	for _, match := range quotePattern.FindAllStringSubmatchIndex(note, -1) {
		matches = append(matches, quoteMatch{Text: note[match[2]:match[3]], Start: offsets[match[0]], End: offsets[match[1]]})
	}
	for _, match := range asciiQuotePattern.FindAllStringSubmatchIndex(note, -1) {
		start, end := offsets[match[0]], offsets[match[1]]
		inside := false
		for _, existing := range matches {
			if start >= existing.Start && end <= existing.End {
				inside = true
				break
			}
		}
		if !inside {
			matches = append(matches, quoteMatch{Text: note[match[2]:match[3]], Start: start, End: end})
		}
	}
	sort.SliceStable(matches, func(left, right int) bool { return matches[left].Start < matches[right].Start })
	return matches
}

var annotationPattern = regexp.MustCompile(`[（(【\[][^）)】\]]{0,24}[）)】\]]`)
var ellipsisPattern = regexp.MustCompile(`\.{3,}|…+|⋯+`)

const fillers = "啊呢吧哈嘛呀哦嗯呃诶哎"

func normalizeQuote(value string) []rune {
	value = annotationPattern.ReplaceAllString(value, "")
	result := []rune{}
	for _, char := range strings.ToLower(value) {
		if unicode.IsSpace(char) || unicode.IsPunct(char) || strings.ContainsRune(fillers, char) ||
			strings.ContainsRune("“”‘’「」『』《》〈〉", char) {
			continue
		}
		result = append(result, char)
	}
	return result
}

func quoteSegments(quote string) [][]rune {
	segments := [][]rune{}
	for _, part := range ellipsisPattern.Split(quote, -1) {
		if normalized := normalizeQuote(part); len(normalized) >= 2 {
			segments = append(segments, normalized)
		}
	}
	return segments
}

type cueIndex struct {
	cues  []cue
	text  []rune // 规范化后的拼接文本
	owner []int  // text 中每个字符所属的片段下标
}

func buildCueIndex(cues []cue) cueIndex {
	index := cueIndex{cues: cues}
	for position, item := range cues {
		for _, char := range normalizeQuote(item.Text) {
			index.text = append(index.text, char)
			index.owner = append(index.owner, position)
		}
	}
	return index
}

// bestMatch 用半全局编辑距离在 haystack 中找与 needle 最接近的子串，返回距离和结束位置。
func bestMatch(needle, haystack []rune) (int, int, int) {
	if len(needle) == 0 || len(haystack) == 0 {
		return len(needle), -1, -1
	}
	previous := make([]int, len(haystack)+1)
	starts := make([]int, len(haystack)+1)
	current := make([]int, len(haystack)+1)
	currentStarts := make([]int, len(haystack)+1)
	for column := range previous {
		starts[column] = column
	}
	for row := 1; row <= len(needle); row++ {
		current[0] = row
		currentStarts[0] = 0
		for column := 1; column <= len(haystack); column++ {
			cost := 1
			if needle[row-1] == haystack[column-1] {
				cost = 0
			}
			best, start := previous[column-1]+cost, starts[column-1]
			if value := previous[column] + 1; value < best {
				best, start = value, starts[column]
			}
			if value := current[column-1] + 1; value < best {
				best, start = value, currentStarts[column-1]
			}
			current[column], currentStarts[column] = best, start
		}
		previous, current = current, previous
		starts, currentStarts = currentStarts, starts
	}
	distance, end := previous[0], 0
	for column := 1; column <= len(haystack); column++ {
		if previous[column] < distance {
			distance, end = previous[column], column
		}
	}
	return distance, starts[end], end
}

// windowRange 返回规范化文本中某时间点 ±radius 秒范围对应的区间。
func (index cueIndex) windowRange(seconds, radius int) (int, int) {
	start, end := -1, -1
	for position, owner := range index.owner {
		at := index.cues[owner].Start
		if at >= seconds-radius && at <= seconds+radius {
			if start < 0 {
				start = position
			}
			end = position + 1
		}
	}
	return start, end
}

type segmentHit struct {
	distance int
	from     int
	to       int
}

func matchSegments(segments [][]rune, text []rune, offset int) ([]segmentHit, bool, bool) {
	hits := []segmentHit{}
	exact, close := true, true
	total, errors := 0, 0
	ordered := true
	for _, segment := range segments {
		distance, from, to := bestMatch(segment, text)
		if to < 0 {
			return nil, false, false
		}
		if len(hits) > 0 && from+offset < hits[len(hits)-1].to {
			ordered = false
		}
		hits = append(hits, segmentHit{distance, from + offset, to + offset})
		total += len(segment)
		errors += distance
		if distance > 0 {
			exact = false
		}
		if len(segment) == 0 || float64(distance)/float64(len(segment)) > 0.2 {
			close = false
		}
	}
	if !ordered || total == 0 || float64(errors)/float64(total) > 0.2 {
		close = false
	}
	return hits, exact, close
}

func (index cueIndex) describe(hits []segmentHit) (string, string) {
	if len(hits) == 0 {
		return "", ""
	}
	// 每段命中各自取所在字幕；不相邻的段之间用“……”分开，便于逐段对照。
	type span struct {
		first, last int
		truncated   bool
	}
	spans := []span{}
	for _, hit := range hits {
		if hit.to <= hit.from {
			continue
		}
		first, last := index.owner[hit.from], index.owner[hit.to-1]
		truncated := false
		if last-first > 4 {
			last, truncated = first+4, true
		}
		spans = append(spans, span{first, last, truncated})
	}
	if len(spans) == 0 {
		return "", ""
	}
	sort.SliceStable(spans, func(left, right int) bool { return spans[left].first < spans[right].first })
	merged := []span{spans[0]}
	for _, item := range spans[1:] {
		tail := &merged[len(merged)-1]
		if item.first <= tail.last+1 {
			if item.last > tail.last {
				tail.last = item.last
			}
			tail.truncated = tail.truncated || item.truncated
			continue
		}
		merged = append(merged, item)
	}
	parts := []string{}
	for _, item := range merged {
		texts := []string{}
		for _, cue := range index.cues[item.first : item.last+1] {
			texts = append(texts, cue.Text)
		}
		part := strings.Join(texts, " ")
		if item.truncated {
			part += "……"
		}
		parts = append(parts, part)
	}
	return clockText(index.cues[merged[0].first].Start), strings.Join(parts, " …… ")
}

func (index cueIndex) hitStart(hits []segmentHit) int {
	if len(hits) == 0 || hits[0].from < 0 || hits[0].from >= len(index.owner) {
		return -1
	}
	return index.cues[index.owner[hits[0].from]].Start
}

func checkQuote(quote string, cited int, index cueIndex) QuoteCheck {
	result := QuoteCheck{Quote: quote, Status: "unverified"}
	if cited >= 0 {
		result.CitedAt = clockText(cited)
	}
	segments := quoteSegments(quote)
	if len(segments) == 0 || len(index.text) == 0 {
		return result
	}
	hasTime := len(index.cues) > 0 && index.cues[0].Start >= 0
	var window *QuoteCheck
	if hasTime && cited >= 0 {
		if from, to := index.windowRange(cited, 90); from >= 0 {
			hits, exact, close := matchSegments(segments, index.text[from:to], from)
			if exact {
				result.At, result.Original = index.describe(hits)
				result.Status = "exact"
				return result
			}
			if close {
				candidate := result
				candidate.At, candidate.Original = index.describe(hits)
				candidate.Status = "edited"
				window = &candidate
			}
		}
	}
	hits, exact, close := matchSegments(segments, index.text, 0)
	if exact || close {
		result.At, result.Original = index.describe(hits)
		status := map[bool]string{true: "exact", false: "edited"}[exact]
		if hasTime && cited >= 0 {
			start := index.hitStart(hits)
			if start < 0 || start < cited-90 || start > cited+90 {
				status = "moved"
			}
		}
		result.Status = status
		return result
	}
	if window != nil {
		return *window
	}
	return result
}

// nearestCitation 取引号之后（优先）或之前 40 字以内的引用。
func nearestCitation(refs []citationRef, quoteStart, quoteEnd int) *citationRef {
	var best *citationRef
	bestGap := 41
	for index := range refs {
		ref := &refs[index]
		gap := -1
		if ref.Start >= quoteEnd {
			gap = ref.Start - quoteEnd
		} else if ref.End <= quoteStart {
			gap = quoteStart - ref.End + 1
		}
		if gap >= 0 && gap < bestGap {
			best, bestGap = ref, gap
		}
	}
	return best
}

func timeNear(runes []rune, from, to int) int {
	if from < 0 {
		from = 0
	}
	if to > len(runes) {
		to = len(runes)
	}
	if from >= to {
		return -1
	}
	return parseTime(string(runes[from:to]))
}

const maxQuoteChecks = 200

func quoteStatusRank(status string) int {
	switch status {
	case "exact":
		return 3
	case "edited":
		return 2
	case "moved":
		return 1
	default:
		return 0
	}
}

// CheckQuotes 逐条核对回答中带来源编号的引号原话。
func CheckQuotes(note string, sources []TaskSource) []QuoteCheck {
	refs := findCitations(note)
	runes := []rune(note)
	indexes := map[string]cueIndex{}
	for _, source := range sources {
		if source.Read {
			indexes[source.SourceID] = buildCueIndex(viewCues(source.Text))
		}
	}
	checks := []QuoteCheck{}
	for _, match := range findQuotes(note) {
		if len(checks) >= maxQuoteChecks {
			break
		}
		quote := match.Text
		if len(normalizeQuote(quote)) < 6 {
			continue
		}
		start, end := match.Start, match.End
		ref := nearestCitation(refs, start, end)
		if ref == nil || len(ref.IDs) == 0 {
			continue
		}
		cited := ref.Time
		if cited < 0 {
			cited = timeNear(runes, end, end+48)
			if ref.Start >= end {
				if value := timeNear(runes, ref.End, ref.End+48); cited < 0 {
					cited = value
				}
			}
		}
		var chosen QuoteCheck
		bestRank := -1
		for _, id := range ref.IDs {
			index, ok := indexes[id]
			if !ok {
				continue
			}
			check := checkQuote(quote, cited, index)
			check.SourceID = id
			rank := quoteStatusRank(check.Status)
			if chosen.SourceID == "" || rank > bestRank {
				chosen, bestRank = check, rank
			}
			if bestRank == 3 {
				break
			}
		}
		if chosen.SourceID != "" {
			checks = append(checks, chosen)
		}
	}
	return checks
}

var quoteStatusLabels = map[string]string{"exact": "原文一致", "edited": "有删改", "moved": "时间不符", "unverified": "待核对"}

// quoteMarkdown 把逐句核对结果写进导出的研究成果。
func quoteMarkdown(checks []QuoteCheck) string {
	if len(checks) == 0 {
		return ""
	}
	summary, _ := quoteSummary(checks)
	lines := []string{"## 原话核对", "", summary, ""}
	for _, check := range checks {
		line := fmt.Sprintf("- 【%s】“%s” —— %s", quoteStatusLabels[check.Status], check.Quote, check.SourceID)
		if check.At != "" {
			line += " 字幕 " + check.At
		}
		if check.Status == "moved" && check.CitedAt != "" {
			line += "（回答标注 " + check.CitedAt + "）"
		}
		if check.Original != "" {
			label := "原文"
			if check.At != "" {
				label = "原始字幕"
			}
			line += "；" + label + "：" + check.Original
		} else {
			line += "；所读原文中找不到这句话"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func quoteSummary(checks []QuoteCheck) (string, int) {
	counts := map[string]int{}
	for _, check := range checks {
		counts[check.Status]++
	}
	if len(checks) == 0 {
		return "回答中没有可核对的带来源编号引号原话（无编号或过长的引号不在此列）", 0
	}
	return fmt.Sprintf("原话逐句核对：%d 条逐字一致，%d 条有删改，%d 条时间不符，%d 条待核对",
		counts["exact"], counts["edited"], counts["moved"], counts["unverified"]), counts["unverified"]
}
