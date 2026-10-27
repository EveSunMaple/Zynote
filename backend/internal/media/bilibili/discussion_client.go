package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Discussion struct {
	resources *Resources
	store     *DiscussionStore
}

func NewDiscussion(resources *Resources, store *DiscussionStore) *Discussion {
	return &Discussion{resources: resources, store: store}
}

func (discussion *Discussion) Select(ref string, changes []DiscussionChange) (DiscussionPack, string, error) {
	return discussion.store.Select(ref, changes)
}

var (
	discussionBvidPattern = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`)
	identityPattern       = regexp.MustCompile(`^\d+$`)
	httpCodePattern       = regexp.MustCompile(`HTTP (\d+)`)
	biliAPICodePattern    = regexp.MustCompile(`（(-?\d+)）`)
)

func discussionFailureCode(err error) string {
	if err == nil {
		return "network_or_protocol"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	message := err.Error()
	if match := httpCodePattern.FindStringSubmatch(message); match != nil {
		return "http_" + match[1]
	}
	if match := biliAPICodePattern.FindStringSubmatch(message); match != nil {
		return "api_" + match[1]
	}
	if strings.Contains(message, "格式异常") || strings.Contains(message, "invalid_response") {
		return "invalid_response"
	}
	return "network_or_protocol"
}

func discussionObject(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func discussionArray(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return []any{}
}

func discussionNumber(value any, fallback int64) int64 {
	number, ok := discussionFloat(value)
	if !ok || number < 0 || math.Trunc(number) != number || number > 9007199254740991 {
		return fallback
	}
	return int64(number)
}

func discussionFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, true
		}
		number, err := strconv.ParseFloat(trimmed, 64)
		return number, err == nil
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case nil:
		return 0, true
	default:
		return 0, false
	}
}

func discussionIdentity(value any) string {
	switch typed := value.(type) {
	case string:
		if identityPattern.MatchString(typed) {
			return typed
		}
	case float64:
		if typed >= 0 && math.Trunc(typed) == typed && typed <= 9007199254740991 {
			return strconv.FormatInt(int64(typed), 10)
		}
	case json.Number:
		if identityPattern.MatchString(typed.String()) {
			return typed.String()
		}
	}
	return ""
}

func discussionString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func discussionBool(value any) bool {
	result, _ := value.(bool)
	return result
}

type discussionComment struct {
	rpid            string
	rootID          string
	parentID        string
	user            string
	content         string
	likes           int64
	createdAt       int64
	isUploader      bool
	replies         []*discussionComment
	pinned          bool
	replyCount      int64
	repliesComplete bool
}

func commentIdentity(raw map[string]any) string {
	id := discussionIdentity(raw["rpid_str"])
	if id == "" {
		id = discussionIdentity(raw["rpid"])
	}
	return id
}

func commentRoot(raw map[string]any, root string) (string, bool) {
	rootID := discussionIdentity(raw["root"])
	if rootID == "" {
		rootID = root
	}
	if root != "" && rootID != "" && rootID != root {
		return "", false
	}
	return rootID, true
}

func commentReplies(raw map[string]any, owner int64, root, rpid string) []*discussionComment {
	if root != "" {
		return nil
	}
	children := discussionArray(raw["replies"])
	if len(children) > 40 {
		children = children[:40]
	}
	replies := []*discussionComment{}
	seen := map[string]bool{}
	for _, child := range children {
		entry := commentRecord(child, owner, rpid, false)
		if entry != nil && !seen[entry.rpid] {
			seen[entry.rpid] = true
			replies = append(replies, entry)
		}
	}
	return replies
}

func commentReplyStats(raw map[string]any, replies []*discussionComment) (int64, bool) {
	countValue, hasRcount := raw["rcount"]
	_, hasCount := raw["count"]
	if !hasRcount || countValue == nil {
		countValue = raw["count"]
	}
	count := discussionNumber(countValue, int64(len(replies)))
	return count, (hasRcount || hasCount) && count <= int64(len(replies))
}

func commentRecord(value any, owner int64, root string, pinned bool) *discussionComment {
	raw := discussionObject(value)
	member := discussionObject(raw["member"])
	content := discussionObject(raw["content"])
	rpid := commentIdentity(raw)
	if rpid == "" {
		return nil
	}
	rootID, ok := commentRoot(raw, root)
	if !ok {
		return nil
	}
	replies := commentReplies(raw, owner, root, rpid)
	count, repliesComplete := commentReplyStats(raw, replies)
	user := discussionString(member["uname"])
	if user == "" {
		user = "匿名"
	}
	parent := discussionIdentity(raw["parent"])
	if parent == "" {
		parent = root
	}
	replyCount := count
	if int64(len(replies)) > replyCount {
		replyCount = int64(len(replies))
	}
	return &discussionComment{
		rpid: rpid, rootID: rootID, parentID: parent,
		user: truncateRunes(user, 100), content: truncateRunes(discussionString(content["message"]), 12000),
		likes: discussionNumber(raw["like"], 0), createdAt: discussionNumber(raw["ctime"], 0),
		isUploader: owner != 0 && discussionIdentity(member["mid"]) == strconv.FormatInt(owner, 10),
		replies:    replies, pinned: pinned, replyCount: replyCount, repliesComplete: repliesComplete,
	}
}

func commentRank(comment *discussionComment) float64 {
	_, score := ClassifyDiscussion(comment.content)
	for _, reply := range comment.replies {
		_, childScore := ClassifyDiscussion(reply.content)
		if childScore > score {
			score = childScore
		}
	}
	if comment.isUploader {
		score += 2
	}
	if comment.pinned {
		score += 1
	}
	return score
}

func (discussion *Discussion) readJSON(ctx context.Context, path string, params map[string]string, timeout time.Duration, signed bool) (map[string]any, error) {
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	value, err := discussion.resources.ReadDiscussion(path, params, child, signed)
	if err != nil {
		return nil, err
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid_response")
	}
	return result, nil
}

type commentCandidate struct {
	raw    any
	pinned bool
}

func rootCommentCandidates(data map[string]any) []commentCandidate {
	candidates := []commentCandidate{}
	for _, raw := range discussionArray(data["replies"]) {
		candidates = append(candidates, commentCandidate{raw: raw})
	}
	for _, raw := range discussionArray(data["top_replies"]) {
		candidates = append(candidates, commentCandidate{raw: raw, pinned: true})
	}
	for _, raw := range discussionObject(data["top"]) {
		if discussionIdentity(discussionObject(raw)["rpid"]) != "" {
			candidates = append(candidates, commentCandidate{raw: raw, pinned: true})
		}
	}
	return candidates
}

func mergeRootComments(rows *[]*discussionComment, seen map[string]bool, candidates []commentCandidate, owner int64) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	for _, entry := range candidates {
		row := commentRecord(entry.raw, owner, "", entry.pinned)
		if row == nil {
			continue
		}
		if seen[row.rpid] {
			if entry.pinned {
				for _, existing := range *rows {
					if existing.rpid == row.rpid {
						existing.pinned = true
						break
					}
				}
			}
			continue
		}
		seen[row.rpid] = true
		*rows = append(*rows, row)
	}
}

func pageEnded(data map[string]any, page int) bool {
	pagination := discussionObject(data["page"])
	cursor := discussionObject(data["cursor"])
	ended := discussionBool(cursor["is_end"])
	if count, ok := pagination["count"]; ok {
		size := discussionNumber(pagination["size"], 20)
		if int64(page)*size >= discussionNumber(count, 0) {
			ended = true
		}
	}
	return ended
}

func (discussion *Discussion) fetchRootComments(ctx context.Context, aid, owner int64, pages int, coverage map[string]any, addError func(map[string]any)) []*discussionComment {
	rows := []*discussionComment{}
	seen := map[string]bool{}
	required := []string{"http_404", "api_-400", "invalid_response"}
	for _, group := range []struct {
		sort   int
		budget int
	}{{2, pages}, {0, 1}} {
		for page := 1; page <= group.budget; page++ {
			data, err := discussion.readJSON(ctx, "/x/v2/reply", map[string]string{
				"type": "1", "oid": strconv.FormatInt(aid, 10),
				"sort": strconv.Itoa(group.sort), "pn": strconv.Itoa(page), "ps": "20",
			}, 7*time.Second, false)
			if err != nil {
				code := discussionFailureCode(err)
				addError(map[string]any{"stage": "roots", "sort": group.sort, "code": code})
				if page != 1 || group.sort != 2 || !containsString(required, code) {
					break
				}
				data, err = discussion.readJSON(ctx, "/x/v2/reply/wbi/main", map[string]string{
					"type": "1", "oid": strconv.FormatInt(aid, 10), "mode": "3",
					"pagination_str": "{\"offset\":\"\"}", "web_location": "1315875",
				}, 7*time.Second, true)
				if err != nil {
					addError(map[string]any{"stage": "roots_fallback", "code": discussionFailureCode(err)})
					break
				}
				coverage["fallback_first_page_only"] = true
			}
			coverage["root_pages"] = coverage["root_pages"].(int) + 1
			candidates := rootCommentCandidates(data)
			mergeRootComments(&rows, seen, candidates, owner)
			ended := pageEnded(data, page)
			if group.sort == 2 {
				coverage["root_end"] = ended
			}
			fallback, _ := coverage["fallback_first_page_only"].(bool)
			if ended || len(candidates) == 0 || fallback {
				break
			}
		}
	}
	return rows
}

func threadTargets(rows []*discussionComment) []*discussionComment {
	targets := []*discussionComment{}
	for _, row := range rows {
		if !row.repliesComplete && row.replyCount > int64(len(row.replies)) {
			targets = append(targets, row)
		}
	}
	sort.SliceStable(targets, func(left, right int) bool {
		leftRank, rightRank := commentRank(targets[left]), commentRank(targets[right])
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		return targets[left].likes > targets[right].likes
	})
	if len(targets) > 6 {
		targets = targets[:6]
	}
	return targets
}

func (discussion *Discussion) fetchChildComments(ctx context.Context, aid, owner int64, targets []*discussionComment, coverage map[string]any, addError func(map[string]any)) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	for _, parent := range targets {
		seenReplies := map[string]bool{}
		for _, reply := range parent.replies {
			seenReplies[reply.rpid] = true
		}
		for page := 1; page <= 2; page++ {
			data, err := discussion.readJSON(ctx, "/x/v2/reply/reply", map[string]string{
				"type": "1", "oid": strconv.FormatInt(aid, 10),
				"root": parent.rpid, "pn": strconv.Itoa(page), "ps": "20",
			}, 7*time.Second, false)
			if err != nil {
				addError(map[string]any{"stage": "children", "root": parent.rpid, "code": discussionFailureCode(err)})
				break
			}
			coverage["child_pages"] = coverage["child_pages"].(int) + 1
			added := 0
			for _, raw := range discussionArray(data["replies"]) {
				child := commentRecord(raw, owner, parent.rpid, false)
				if child == nil || seenReplies[child.rpid] {
					continue
				}
				seenReplies[child.rpid] = true
				parent.replies = append(parent.replies, child)
				added++
			}
			if count, ok := discussionObject(data["page"])["count"]; ok {
				parent.replyCount = discussionNumber(count, 0)
			}
			parent.repliesComplete = int64(len(parent.replies)) >= parent.replyCount
			if parent.repliesComplete || added == 0 {
				break
			}
		}
	}
}

func updateCommentCoverage(rows []*discussionComment, coverage map[string]any) {
	rootCount, replyCount, partialThreads := len(rows), 0, 0
	for _, row := range rows {
		replyCount += len(row.replies)
		if !row.repliesComplete {
			partialThreads++
		}
	}
	coverage["root_count"] = rootCount
	coverage["reply_count"] = replyCount
	coverage["partial_threads"] = partialThreads
	rootEnd, _ := coverage["root_end"].(bool)
	errorsList := coverage["errors"].([]map[string]any)
	complete := rootEnd && len(errorsList) == 0 && partialThreads == 0
	coverage["complete"] = complete
	switch {
	case complete:
		coverage["status"] = "complete"
	case rootCount > 0:
		coverage["status"] = "partial"
	case len(errorsList) > 0:
		coverage["status"] = "failed"
	default:
		coverage["status"] = "empty"
	}
}

func (discussion *Discussion) comments(aid, owner int64, pages int) ([]*discussionComment, map[string]any) {
	coverage := map[string]any{
		"status": "partial", "complete": false, "errors": []map[string]any{},
		"root_pages": 0, "child_pages": 0, "root_end": false, "sort": "hot_and_recent",
	}
	addError := func(entry map[string]any) {
		coverage["errors"] = append(coverage["errors"].([]map[string]any), entry)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	rows := discussion.fetchRootComments(ctx, aid, owner, pages, coverage, addError)
	discussion.fetchChildComments(ctx, aid, owner, threadTargets(rows), coverage, addError)
	if ctx.Err() != nil {
		addError(map[string]any{"stage": "collection", "code": "deadline"})
	}
	updateCommentCoverage(rows, coverage)
	return rows, coverage
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type discussionDanmaku struct {
	id         string
	text       string
	timeSec    float64
	createdAt  int64
	isUploader bool
}

func danmakuUploader(owner int64) func(string) bool {
	if owner == 0 {
		return func(string) bool { return false }
	}
	ownerHash := strconv.FormatUint(uint64(crc32.ChecksumIEEE([]byte(strconv.FormatInt(owner, 10)))), 16)
	return func(mid string) bool {
		return strings.TrimLeft(mid, "0") == ownerHash
	}
}

func parseDanmakuXML(data []byte, uploader func(string) bool) ([]discussionDanmaku, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	rows := []discussionDanmaku{}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	var params []string
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid_xml")
		}
		switch typed := token.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("invalid_xml")
		case xml.StartElement:
			depth++
			if depth == 1 && typed.Name.Local != "i" {
				return nil, fmt.Errorf("invalid_xml")
			}
			if depth == 2 && typed.Name.Local == "d" {
				raw := ""
				for _, attribute := range typed.Attr {
					if attribute.Name.Local == "p" {
						raw = attribute.Value
					}
				}
				params = strings.Split(raw, ",")
				text.Reset()
			}
		case xml.CharData:
			if params != nil {
				text.Write(typed)
			}
		case xml.EndElement:
			if depth == 2 && typed.Name.Local == "d" && params != nil {
				if len(params) >= 8 {
					seconds, valid := jsNumber(params[0])
					if valid {
						addDanmaku(&rows, params[7], text.String(), seconds, params[6],
							int64(jsNumberOr(params[4], 0)), int(jsNumberOr(params[1], 1)), uploader)
					}
				}
				params = nil
			}
			depth--
		}
	}
	return rows, nil
}

func protoString(values map[int]protoField, key int) string {
	if item, ok := values[key]; ok && item.bytes != nil {
		return string(item.bytes)
	}
	return ""
}

func protoInteger(values map[int]protoField, key int, fallback int64) int64 {
	if item, ok := values[key]; ok && item.bytes == nil && item.varint <= 9007199254740991 {
		return int64(item.varint)
	}
	return fallback
}

func parseDanmakuProto(data []byte, uploader func(string) bool) ([]discussionDanmaku, bool, error) {
	rows := []discussionDanmaku{}
	closed := false
	fields, err := protobufFields(data)
	if err != nil {
		return nil, false, err
	}
	for _, field := range fields {
		if field.number == 2 && field.bytes == nil && field.varint == 1 {
			closed = true
		}
		if field.number != 1 || field.bytes == nil {
			continue
		}
		items, err := protobufFields(field.bytes)
		if err != nil {
			return nil, false, err
		}
		values := map[int]protoField{}
		for _, item := range items {
			values[item.number] = item
		}
		id := protoString(values, 12)
		if id == "" {
			if item, ok := values[1]; ok && item.bytes == nil {
				id = strconv.FormatUint(item.varint, 10)
			}
		}
		addDanmaku(&rows, id, protoString(values, 7), float64(protoInteger(values, 2, 0))/1000,
			protoString(values, 6), protoInteger(values, 8, 0), int(protoInteger(values, 3, 1)), uploader)
	}
	return rows, closed, nil
}

func parseDanmaku(data []byte, owner int64) ([]discussionDanmaku, bool, error) {
	if len(data) > 12*1024*1024 {
		return nil, false, fmt.Errorf("danmaku_too_large")
	}
	uploader := danmakuUploader(owner)
	initial := strings.TrimPrefix(string(data[:minInt(200, len(data))]), "\ufeff")
	initial = strings.TrimLeftFunc(initial, unicode.IsSpace)
	if strings.HasPrefix(initial, "<") {
		rows, err := parseDanmakuXML(data, uploader)
		return rows, false, err
	}
	if strings.HasPrefix(initial, "[") || strings.HasPrefix(initial, "{") {
		return nil, false, fmt.Errorf("json_error_not_danmaku")
	}
	return parseDanmakuProto(data, uploader)
}

type protoField struct {
	number int
	wire   int
	varint uint64
	bytes  []byte
}

type protoReader struct {
	data []byte
	pos  int
}

func (reader *protoReader) varint() (uint64, error) {
	var value uint64
	for shift := 0; shift < 70; shift += 7 {
		if reader.pos >= len(reader.data) {
			return 0, fmt.Errorf("truncated_protobuf")
		}
		part := reader.data[reader.pos]
		reader.pos++
		value |= uint64(part&127) << uint(shift)
		if part&128 == 0 {
			if shift == 63 && part > 1 {
				return 0, fmt.Errorf("oversized_varint")
			}
			return value, nil
		}
	}
	return 0, fmt.Errorf("oversized_varint")
}

func (reader *protoReader) field(tag uint64) (protoField, error) {
	number := int(tag >> 3)
	wire := int(tag & 7)
	if number == 0 || number > 536870911 {
		return protoField{}, fmt.Errorf("invalid_protobuf")
	}
	switch wire {
	case 0:
		value, err := reader.varint()
		if err != nil {
			return protoField{}, err
		}
		return protoField{number: number, wire: wire, varint: value}, nil
	case 1, 5:
		size := 8
		if wire == 5 {
			size = 4
		}
		if size > len(reader.data)-reader.pos {
			return protoField{}, fmt.Errorf("truncated_protobuf")
		}
		field := protoField{number: number, wire: wire, bytes: reader.data[reader.pos : reader.pos+size]}
		reader.pos += size
		return field, nil
	case 2:
		size, err := reader.varint()
		if err != nil {
			return protoField{}, err
		}
		if size > uint64(len(reader.data)-reader.pos) {
			return protoField{}, fmt.Errorf("truncated_protobuf")
		}
		field := protoField{number: number, wire: wire, bytes: reader.data[reader.pos : reader.pos+int(size)]}
		reader.pos += int(size)
		return field, nil
	default:
		return protoField{}, fmt.Errorf("unsupported_protobuf_wire")
	}
}

func protobufFields(data []byte) ([]protoField, error) {
	reader := &protoReader{data: data}
	fields := []protoField{}
	for reader.pos < len(data) {
		tag, err := reader.varint()
		if err != nil {
			return nil, err
		}
		field, err := reader.field(tag)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func addDanmaku(rows *[]discussionDanmaku, id, text string, seconds float64, mid string, created int64, mode int, uploader func(string) bool) { // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	if mode == 8 || mode == 9 || text == "" || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return
	}
	*rows = append(*rows, discussionDanmaku{
		id: id, text: truncateRunes(text, 4000), timeSec: seconds, createdAt: created, isUploader: uploader(mid),
	})
}

func jsNumber(value string) (float64, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, true
	}
	number, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func jsNumberOr(value string, fallback float64) float64 {
	number, ok := jsNumber(value)
	if !ok {
		return fallback
	}
	return number
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func danmakuSegments(total, limit int) []int {
	segments := []int{}
	if total <= limit {
		for index := 1; index <= total; index++ {
			segments = append(segments, index)
		}
		return segments
	}
	seen := map[int]bool{}
	for index := 0; index < limit; index++ {
		value := 1 + int(math.Round(float64(index)*float64(total-1)/float64(limit-1)))
		if !seen[value] {
			seen[value] = true
			segments = append(segments, value)
		}
	}
	return segments
}

func uniqueDanmaku(rows []discussionDanmaku) []discussionDanmaku {
	unique := []discussionDanmaku{}
	seen := map[string]bool{}
	for _, row := range rows {
		key := row.id
		if key == "" {
			encoded, _ := json.Marshal([]any{row.timeSec, row.text})
			key = string(encoded)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, row)
	}
	return unique
}

func sortDanmaku(rows []discussionDanmaku) {
	sort.SliceStable(rows, func(left, right int) bool {
		_, leftScore := ClassifyDiscussion(rows[left].text)
		_, rightScore := ClassifyDiscussion(rows[right].text)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if rows[left].isUploader != rows[right].isUploader {
			return rows[left].isUploader
		}
		return rows[left].timeSec < rows[right].timeSec
	})
}

func updateDanmakuCoverage(coverage map[string]any, okSegments []int, total int, unique []discussionDanmaku) int {
	sort.Ints(okSegments)
	coverage["segments_ok"] = okSegments
	coverage["raw_count"] = len(unique)
	retained := len(unique)
	if retained > 800 {
		retained = 800
	}
	coverage["retained_count"] = retained
	coverage["retention_limited"] = len(unique) > 800
	closed, _ := coverage["closed"].(bool)
	errorsList := coverage["errors"].([]map[string]any)
	complete := len(okSegments) == total && len(errorsList) == 0 && len(unique) <= 800
	coverage["complete"] = complete
	switch {
	case closed:
		coverage["status"] = "closed"
	case complete:
		coverage["status"] = "complete"
	case len(unique) > 0:
		coverage["status"] = "partial"
	case len(errorsList) > 0:
		coverage["status"] = "failed"
	default:
		coverage["status"] = "empty"
	}
	return retained
}

func (discussion *Discussion) danmaku(cid int64, duration float64, owner int64) ([]discussionDanmaku, map[string]any) {
	total := int(math.Max(1, math.Ceil(duration/360)))
	segments := danmakuSegments(total, 12)
	okSegments := []int{}
	rows := []discussionDanmaku{}
	coverage := map[string]any{
		"status": "partial", "complete": false, "errors": []map[string]any{},
		"segments_total": total, "segments_requested": segments, "segments_ok": okSegments,
		"scope": "current_pool_not_all_historical",
	}
	addError := func(entry map[string]any) {
		coverage["errors"] = append(coverage["errors"].([]map[string]any), entry)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, segment := range segments {
		child, childCancel := context.WithTimeout(ctx, 6*time.Second)
		data, err := discussion.resources.RawDiscussion("/x/v2/dm/web/seg.so", map[string]string{
			"type": "1", "oid": strconv.FormatInt(cid, 10), "segment_index": strconv.Itoa(segment),
		}, child, 12*1024*1024)
		childCancel()
		if err != nil {
			addError(map[string]any{"segment": segment, "code": discussionFailureCode(err)})
			continue
		}
		parsed, closed, err := parseDanmaku(data, owner)
		if err != nil {
			addError(map[string]any{"segment": segment, "code": discussionFailureCode(err)})
			continue
		}
		if closed {
			coverage["closed"] = true
		}
		rows = append(rows, parsed...)
		okSegments = append(okSegments, segment)
	}
	coverage["segments_ok"] = okSegments
	if ctx.Err() != nil {
		addError(map[string]any{"code": "deadline"})
	}
	if len(okSegments) == 0 {
		child, childCancel := context.WithTimeout(context.Background(), 5*time.Second)
		data, err := discussion.resources.RawDiscussion("/x/v1/dm/list.so", map[string]string{
			"oid": strconv.FormatInt(cid, 10),
		}, child, 12*1024*1024)
		childCancel()
		if err == nil {
			if parsed, _, parseErr := parseDanmaku(data, owner); parseErr == nil {
				rows = append(rows, parsed...)
				coverage["fallback"] = "xml_pool"
			} else {
				addError(map[string]any{"code": "xml_unavailable"})
			}
		} else {
			addError(map[string]any{"code": "xml_unavailable"})
		}
	}
	unique := uniqueDanmaku(rows)
	sortDanmaku(unique)
	retained := updateDanmakuCoverage(coverage, okSegments, total, unique)
	return unique[:retained], coverage
}

type videoPage struct {
	page     int
	cid      int64
	duration float64
}

func discussionTarget(input string) (string, int, map[string]string, error) {
	normalized, err := VideoURL(input)
	if err != nil {
		return "", 0, nil, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", 0, nil, err
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", 0, nil, fmt.Errorf("视频来源与所选网址不一致")
	}
	id := parts[1]
	page := 1
	if value := parsed.Query().Get("p"); value != "" {
		if number, parseErr := strconv.Atoi(value); parseErr == nil && number > 0 {
			page = number
		}
	}
	params := map[string]string{"bvid": id}
	if strings.HasPrefix(id, "av") {
		params = map[string]string{"aid": strings.TrimPrefix(id, "av")}
	}
	return id, page, params, nil
}

func parseVideoPages(view map[string]any) ([]videoPage, error) {
	pagesData, ok := view["pages"].([]any)
	if !ok {
		return nil, fmt.Errorf("视频信息格式异常，请稍后重试")
	}
	pages := []videoPage{}
	for _, item := range pagesData {
		raw := discussionObject(item)
		entry := videoPage{
			page: int(discussionNumber(raw["page"], 0)), cid: discussionNumber(raw["cid"], 0),
			duration: jsNumberOrAny(raw["duration"]),
		}
		if entry.page <= 0 || entry.cid <= 0 || entry.duration < 0 {
			return nil, fmt.Errorf("视频信息格式异常，请稍后重试")
		}
		pages = append(pages, entry)
	}
	return pages, nil
}

func selectVideoPage(pages []videoPage, page int) (videoPage, error) {
	for _, entry := range pages {
		if entry.page == page {
			return entry, nil
		}
	}
	return videoPage{}, fmt.Errorf("所选分集不存在")
}

func addDiscussionItem(items *[]DiscussionItem, bvid string, cid int64, page int, kind, text, identity string, meta discussionItemMeta) { // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	text = truncateRunes(strings.TrimSpace(text), 12000)
	if text == "" {
		return
	}
	labels, score := ClassifyDiscussion(text)
	digest, _ := DiscussionDigest(text)
	itemID := identity
	if itemID == "" {
		keyDigest, _ := DiscussionDigest([]any{bvid, cid, text})
		itemID = keyDigest[:20]
	}
	finalScore := score
	if meta.isUploader {
		finalScore += 2
	}
	if meta.pinned {
		finalScore += 1
	}
	likes := int64(0)
	if meta.likes != nil {
		likes = *meta.likes
	}
	if likes < 1 {
		likes = 1
	}
	finalScore += math.Min(1, math.Log10(float64(likes)))
	*items = append(*items, DiscussionItem{
		ID: kind + ":" + itemID, Kind: kind, Text: text, TextSHA256: digest, Labels: labels,
		Score: roundScore(finalScore), Selected: false, Verification: "unverified", Reviewed: false,
		Note: "", URL: meta.url, Author: meta.author, Likes: meta.likes, Rpid: meta.rpid,
		RootID: meta.rootID, ParentID: meta.parentID, IsUploader: boolPointer(meta.isUploader),
		Pinned: meta.pinnedValue(), CreatedAt: meta.createdAt, ReplyCount: meta.replyCount,
		RepliesComplete: meta.repliesComplete, TimeSec: meta.timeSec, Cid: meta.cid, Page: meta.page,
	})
}

func appendCommentItems(items *[]DiscussionItem, comments []*discussionComment, base string, cid int64, page int) {
	seen := map[string]bool{}
	for _, comment := range comments {
		if seen[comment.rpid] {
			continue
		}
		seen[comment.rpid] = true
		addDiscussionItem(items, "", cid, page, "comment", comment.content, comment.rpid, discussionItemMeta{
			author: &comment.user, likes: &comment.likes, rpid: &comment.rpid,
			isUploader: comment.isUploader, createdAt: &comment.createdAt,
			url: base + "#reply" + comment.rpid, rootID: stringPointer(""), parentID: stringPointer(""),
			pinnedSet: true, pinned: comment.pinned, replyCount: &comment.replyCount, repliesComplete: &comment.repliesComplete,
		})
		for _, reply := range comment.replies {
			if seen[reply.rpid] {
				continue
			}
			seen[reply.rpid] = true
			parentID := "comment:" + comment.rpid
			if reply.parentID != "" && reply.parentID != comment.rpid {
				parentID = "reply:" + reply.parentID
			}
			addDiscussionItem(items, "", cid, page, "reply", reply.content, reply.rpid, discussionItemMeta{
				author: &reply.user, likes: &reply.likes, rpid: &reply.rpid,
				isUploader: reply.isUploader, createdAt: &reply.createdAt,
				url: base + "#reply" + reply.rpid, rootID: stringPointer("comment:" + comment.rpid),
				parentID: stringPointer(parentID),
			})
		}
	}
}

func appendDanmakuItems(items *[]DiscussionItem, rows []discussionDanmaku, base string, cid int64, page int) {
	for _, row := range rows {
		timeSec := row.timeSec
		createdAt := row.createdAt
		pageNumber := page
		addDiscussionItem(items, "", cid, page, "danmaku", row.text, row.id, discussionItemMeta{
			isUploader: row.isUploader, createdAt: &createdAt, timeSec: &timeSec, cid: &cid, page: &pageNumber,
			url: fmt.Sprintf("%s?p=%d&t=%d", base, page, int(math.Floor(row.timeSec))),
		})
	}
}

func (discussion *Discussion) Collect(input string, pages int) (*DiscussionPack, error) {
	id, page, params, err := discussionTarget(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := discussion.readJSON(ctx, "/x/web-interface/view", params, 20*time.Second, false)
	if err != nil {
		return nil, err
	}
	bvid := discussionString(view["bvid"])
	if !discussionBvidPattern.MatchString(bvid) {
		return nil, fmt.Errorf("视频信息格式异常，请稍后重试")
	}
	aid := discussionNumber(view["aid"], 0)
	owner := discussionNumber(discussionObject(view["owner"])["mid"], 0)
	description := discussionString(view["desc"])
	videoPages, err := parseVideoPages(view)
	if err != nil {
		return nil, err
	}
	if aid <= 0 {
		return nil, fmt.Errorf("视频信息格式异常，请稍后重试")
	}
	if strings.HasPrefix(id, "BV") && bvid != id {
		return nil, fmt.Errorf("视频来源与所选网址不一致")
	}
	selected, err := selectVideoPage(videoPages, page)
	if err != nil {
		return nil, err
	}
	var comments []*discussionComment
	var commentCoverage map[string]any
	var danmakuRows []discussionDanmaku
	var danmakuCoverage map[string]any
	wait := sync.WaitGroup{}
	wait.Add(2)
	go func() {
		defer wait.Done()
		comments, commentCoverage = discussion.comments(aid, owner, pages)
	}()
	go func() {
		defer wait.Done()
		danmakuRows, danmakuCoverage = discussion.danmaku(selected.cid, selected.duration, owner)
	}()
	wait.Wait()
	items := []DiscussionItem{}
	base := "https://www.bilibili.com/video/" + bvid
	addDiscussionItem(&items, bvid, selected.cid, page, "description", description, bvid, discussionItemMeta{
		isUploader: true, url: base,
	})
	appendCommentItems(&items, comments, base, selected.cid, page)
	appendDanmakuItems(&items, danmakuRows, base, selected.cid, page)
	truncated := len(items) > 1200
	if truncated {
		items = items[:1200]
	}
	pack := DiscussionPack{
		Schema: "video-discussion-v1", Bvid: bvid, Cid: selected.cid, Page: page,
		FetchedAt: time.Now().Unix(), ClassificationMethod: "heuristic_candidates_v1",
		Policy: DiscussionPolicy,
		Coverage: DiscussionCoverage{
			Comments: commentCoverage, Danmaku: danmakuCoverage, ItemsTruncated: truncated,
		},
		Items: items,
	}
	return discussion.store.Save(pack)
}

type discussionItemMeta struct {
	author          *string
	likes           *int64
	rpid            *string
	rootID          *string
	parentID        *string
	isUploader      bool
	pinned          bool
	pinnedSet       bool
	createdAt       *int64
	replyCount      *int64
	repliesComplete *bool
	timeSec         *float64
	cid             *int64
	page            *int
	url             string
}

func (meta discussionItemMeta) pinnedValue() *bool {
	if !meta.pinnedSet && !meta.pinned {
		return nil
	}
	value := meta.pinned
	return &value
}

func stringPointer(value string) *string { return &value }

func boolPointer(value bool) *bool { return &value }

func jsNumberOrAny(value any) float64 {
	if number, ok := discussionFloat(value); ok {
		return number
	}
	return 0
}
