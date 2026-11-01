package research

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// 多轮研究：模型拿到资料后，如果觉得不够，可以不作答而是请求补充资料（继续读某份被截断的字幕、
// 在知识库或已勾选的外部渠道再检索、调用已选 MCP 工具）；系统执行后带着新资料再问一轮，最多 maxResearchRounds 轮。
// 资料足够时第一轮就直接作答，和单轮研究一样快。最终回答照常逐句回原文核对。

const librarySearchLabel = "知识库检索“"

const (
	maxResearchRounds = 8
	// localReadLimit 是多轮累计读取本地资料的总字数上限（首轮预算 localSourceBudget 之外按需加读）。
	localReadLimit = 400000
	// roundSourceBudget 是检索新增一份本地资料时首次读取的字数。
	roundSourceBudget = 40000
	maxRoundActions   = 3
	maxRoundMaterials = 8
	maxRoundExternal  = 24
)

// roundSearchChannels 是可以在多轮中再次检索的外部渠道（须在本次研究里已勾选）。
var roundSearchChannels = []string{"crossref", "semantic", "arxiv", "openalex", "web", "wikipedia"}

type roundAction struct {
	Action    string         `json:"action"`
	Source    string         `json:"source"`
	Until     string         `json:"until"`
	Query     string         `json:"query"`
	Channel   string         `json:"channel"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

var needBlockPattern = regexp.MustCompile("(?s)^```need[ \\t]*\\r?\\n(.*?)```\\s*$")

// parseRoundRequest 判断模型这一轮是在请求补充资料（只返回一个 ```need 代码块）还是在作答。
// 第二个返回值为真表示是请求；请求格式写坏时动作为空。
func parseRoundRequest(answer string) ([]roundAction, bool) {
	match := needBlockPattern.FindStringSubmatch(strings.TrimSpace(answer))
	if match == nil {
		return nil, false
	}
	var actions []roundAction
	if decodeLenient([]byte(match[1]), &actions) != nil {
		return nil, true
	}
	if len(actions) > maxRoundActions {
		actions = actions[:maxRoundActions]
	}
	return actions, true
}

// readingPlan 记录每份资料读到哪里；每轮补充后据此重建阅读清单（本地资料在前、外部资料在后，编号重新分配）。
// 本地资料始终“从开头读到某处”，保证阅读片段是原文前缀，结果核验规则不变。
type readingPlan struct {
	materials []Source
	views     [][]rune
	caps      []int
	external  []ExternalEvidence
	// extra 是范围说明里资料部分之后的附加说明（如 MCP 工具未调用），重建时保留。
	extra string
}

func newReadingPlan(task *Task) *readingPlan {
	views := sourceViews(task.Materials)
	caps := make([]int, len(views))
	clipped := false
	for index := range views {
		caps[index] = len([]rune(task.Sources[index].Text))
		clipped = clipped || task.Sources[index].Truncated
	}
	base := sourceLimitations(task, clipped, len(task.External) > 0)
	return &readingPlan{
		materials: task.Materials, views: views, caps: caps, external: task.External,
		extra: strings.TrimPrefix(task.Limitations, base),
	}
}

func (plan *readingPlan) apply(task *Task) {
	task.Materials = plan.materials
	task.Sources = []TaskSource{}
	clipped := appendLocalSourcesWithCaps(task, plan.materials, plan.views, plan.caps)
	appendExternalTaskSources(task, plan.external)
	task.Limitations = sourceLimitations(task, clipped, len(task.External) > 0) + plan.extra
}

func (plan *readingPlan) room() int {
	used := 0
	for _, value := range plan.caps {
		used += value
	}
	return localReadLimit - used
}

func (plan *readingPlan) truncated() []int {
	indexes := []int{}
	for index, view := range plan.views {
		if plan.caps[index] < len(view) {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func (plan *readingPlan) hasMaterial(source Source) bool {
	for _, item := range plan.materials {
		if item.Binding.BVID == source.Binding.BVID {
			return true
		}
	}
	return false
}

// roundOptions 是本轮模型可以请求的动作；全都没有时直接要求作答。
type roundOptions struct {
	read     []int
	library  bool
	channels []string
	tools    []selectedMcpTool
}

func (options roundOptions) any() bool {
	return len(options.read) > 0 || options.library || len(options.channels) > 0 || len(options.tools) > 0
}

func (plan *readingPlan) options(parsed *researchCreateInput) roundOptions {
	options := roundOptions{}
	if plan.room() > 0 {
		options.read = plan.truncated()
		options.library = parsed.seen["library"] && len(plan.materials) < maxRoundMaterials
	}
	if len(plan.external) < maxRoundExternal {
		for _, channel := range roundSearchChannels {
			if parsed.seen[channel] {
				options.channels = append(options.channels, channel)
			}
		}
	}
	if parsed.mcp != nil {
		options.tools = parsed.mcp.Tools
	}
	return options
}

// roundGuide 写给模型的多轮规则；只在还能补充资料的轮次附加。
func roundGuide(round int, task *Task, options roundOptions) string {
	lines := []string{
		fmt.Sprintf("\n\n【多轮研究，第 %d/%d 轮】如果现有资料已足够，直接按上面的要求作答。", round, maxResearchRounds),
		"只有回答确实缺资料时，才不作答，而是只返回一个 ```need 代码块，内容是 JSON 数组，每轮最多 3 个动作，不要重复已经做过的动作：",
	}
	if done := roundHistory(task); len(done) > 0 {
		lines = append(lines, "已经做过的补充（不要重复；没有新结果的就换检索词或直接作答）：\n"+strings.Join(done, "\n"))
	}
	if len(options.read) > 0 {
		names := []string{}
		for _, index := range options.read {
			source := task.Sources[index]
			names = append(names, source.SourceID+"《"+source.Title+"》"+source.Coverage)
		}
		lines = append(lines, `- {"action":"read","source":"S1","until":"01:20:00"} 继续读取被截断的资料到某个时刻，"until":"end" 读到结尾。可继续读：`+strings.Join(names, "；"))
	}
	if options.library {
		lines = append(lines, `- {"action":"search_library","query":"关键词"} 在知识库里再找相关资料`)
	}
	if len(options.channels) > 0 {
		lines = append(lines, `- {"action":"search","channel":"渠道","query":"检索词"} 在已勾选的外部渠道再检索（学术渠道用英文术语），可用渠道：`+strings.Join(options.channels, "、"))
	}
	for _, tool := range options.tools {
		schema, _ := json.Marshal(tool.InputSchema)
		lines = append(lines, `- {"action":"tool","tool":"`+tool.ServerID+":"+tool.Name+`","arguments":{...}} 调用外部只读工具 `+tool.Name+"（"+tool.Description+"），参数 schema："+string(schema))
	}
	return strings.Join(lines, "\n")
}

// researchRounds 依次执行各轮：作答则核验发布；请求补充则执行动作、重建阅读清单后进入下一轮。
func (engine *Engine) researchRounds(ctx context.Context, run *runState, task *Task, input map[string]any, parsed *researchCreateInput, snapshot ExtensionSnapshot, contextSnapshot Context) error {
	plan := newReadingPlan(task)
	stalled := false
	for round := 1; ; round++ {
		options := plan.options(parsed)
		final := round >= maxResearchRounds || stalled || !options.any()
		system, user, allowMemory := researchPrompt(task, contextSnapshot, snapshot, parsed)
		label := "资料已交给模型，等待回应"
		if round > 1 {
			label = fmt.Sprintf("第 %d 轮：资料已交给模型，等待回应", round)
		}
		if !final {
			user += roundGuide(round, task, options)
		} else if round > 1 {
			user += "\n\n【多轮研究最后一轮】资料补充已结束，请直接作答，不要再请求资料。"
		}
		answer, err := engine.ask(ctx, run, label, system, user)
		if err != nil {
			return engine.failResearch(task, err)
		}
		actions, requested := parseRoundRequest(answer)
		if !requested {
			return engine.publishAnswer(run, task, answer, allowMemory)
		}
		if final {
			return engine.failResearchMessage(task, "模型在最后一轮仍在请求资料，未给出回答")
		}
		task.Turns++
		run.setStage("collecting", fmt.Sprintf("第 %d 轮：按模型要求补充资料", round))
		stalled = !engine.runRoundActions(ctx, task, plan, parsed, input, round, actions)
		plan.apply(task)
		if err := engine.put(task); err != nil {
			return err
		}
	}
}

// runRoundActions 执行一轮请求的动作，返回是否至少有一个动作带来了新资料或线索。
func (engine *Engine) runRoundActions(ctx context.Context, task *Task, plan *readingPlan, parsed *researchCreateInput, input map[string]any, round int, actions []roundAction) bool {
	if len(actions) == 0 {
		task.Events = append(task.Events, roundEvent(round, "补充请求格式无效，下一轮直接作答"))
		return false
	}
	progressed := false
	for index, action := range actions {
		message, ok := engine.runRoundAction(ctx, task, plan, parsed, input, action, []any{task.TaskID, round, index})
		task.Events = append(task.Events, roundEvent(round, message))
		progressed = progressed || ok
	}
	return progressed
}

// roundHistory 列出前几轮已做过的补充及结果，供模型避免重复请求。
func roundHistory(task *Task) []string {
	done := []string{}
	for _, event := range task.Events {
		if text(event["kind"]) == "round" {
			done = append(done, "- "+text(event["message"]))
		}
	}
	return done
}

func roundEvent(round int, message string) map[string]any {
	return map[string]any{"kind": "round", "message": fmt.Sprintf("第 %d 轮：%s", round, message)}
}

func (engine *Engine) runRoundAction(ctx context.Context, task *Task, plan *readingPlan, parsed *researchCreateInput, input map[string]any, action roundAction, opSeed []any) (string, bool) {
	switch action.Action {
	case "read":
		return plan.read(task, action)
	case "search_library":
		return engine.roundSearchLibrary(plan, parsed, action.Query)
	case "search":
		return engine.roundSearchExternal(ctx, task, plan, parsed, input, action)
	case "tool":
		return engine.roundTool(ctx, task, parsed, action, opSeed)
	}
	return "忽略未知动作 " + trimText(action.Action, 40), false
}

// read 把某份本地资料的阅读范围向后延伸（仍从开头读起），受累计上限约束。
func (plan *readingPlan) read(task *Task, action roundAction) (string, bool) {
	index := sourceIndex(task, action.Source)
	if index < 0 || index >= len(plan.materials) {
		return "只能继续读取知识库资料，忽略 " + trimText(action.Source, 20), false
	}
	view := plan.views[index]
	target := len(view)
	if until := strings.TrimSpace(action.Until); until != "" && until != "end" {
		seconds := parseTime(until)
		if seconds < 0 {
			return "读取终点无效，忽略 " + trimText(until, 20), false
		}
		target = viewOffsetUntil(view, seconds)
	}
	title := "《" + plan.materials[index].Title + "》"
	if target <= plan.caps[index] {
		return title + "所请求的部分已经读过", false
	}
	if room := plan.room(); target-plan.caps[index] > room {
		target = plan.caps[index] + max(room, 0)
	}
	if target <= plan.caps[index] {
		return "累计阅读量已达上限，未再读取" + title, false
	}
	plan.caps[index] = target
	return "继续读取" + title + "，" + coverageText(string(view), string(view[:target])), true
}

func sourceIndex(task *Task, id string) int {
	for index, source := range task.Sources {
		if source.SourceID == strings.TrimSpace(id) {
			return index
		}
	}
	return -1
}

// viewOffsetUntil 返回紧凑字幕中时间不晚于 seconds 的最后一行的结束位置（按行截断，不截半行）。
func viewOffsetUntil(view []rune, seconds int) int {
	offset, end := 0, 0
	for _, line := range strings.SplitAfter(string(view), "\n") {
		offset += len([]rune(line))
		match := compactLinePattern.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if match != nil && clockSeconds(match[1], match[2], match[3]) > seconds {
			break
		}
		end = offset
	}
	return end
}

func (engine *Engine) roundSearchLibrary(plan *readingPlan, parsed *researchCreateInput, query string) (string, bool) {
	query = strings.TrimSpace(query)
	if !parsed.seen["library"] || query == "" {
		return "知识库检索未启用或检索词为空", false
	}
	found, err := engine.Sources.Search(query, nil)
	if err != nil {
		return librarySearchLabel + trimText(query, 40) + "”失败：" + err.Error(), false
	}
	added := []string{}
	for _, source := range found {
		if len(added) >= 3 || len(plan.materials) >= maxRoundMaterials || plan.room() <= 0 {
			break
		}
		if plan.hasMaterial(source) {
			continue
		}
		view := []rune(sourceView(source.Text))
		plan.materials = append(plan.materials, source)
		plan.views = append(plan.views, view)
		plan.caps = append(plan.caps, min(len(view), roundSourceBudget, plan.room()))
		added = append(added, "《"+source.Title+"》")
	}
	if len(added) == 0 {
		return librarySearchLabel + trimText(query, 40) + "”没有新资料", false
	}
	return librarySearchLabel + trimText(query, 40) + "”，新增 " + strings.Join(added, "、"), true
}

func (engine *Engine) roundSearchExternal(ctx context.Context, task *Task, plan *readingPlan, parsed *researchCreateInput, input map[string]any, action roundAction) (string, bool) {
	channel, query := strings.TrimSpace(action.Channel), strings.TrimSpace(action.Query)
	if !parsed.seen[channel] || !contains(roundSearchChannels, channel) || query == "" {
		return "外部渠道 " + trimText(channel, 20) + " 未勾选或检索词为空", false
	}
	external := engine.External
	if external == nil {
		external = NewExternal()
	}
	rows, skipped, err := external.SearchQueries(ctx, []string{channel}, map[string]string{channel: query}, query, input["web_search"])
	task.Events = append(task.Events, externalSkipEvents(skipped)...)
	if err != nil || external.Verify(rows) != nil {
		return channelLabel(channel) + "检索“" + trimText(query, 40) + "”未取得结果", false
	}
	added := 0
	for _, row := range rows {
		if len(plan.external) >= maxRoundExternal || hasExternalURL(plan.external, row.URL) {
			continue
		}
		plan.external = append(plan.external, row)
		added++
	}
	return fmt.Sprintf("%s检索“%s”，新增 %d 条", channelLabel(channel), trimText(query, 40), added), added > 0
}

func hasExternalURL(rows []ExternalEvidence, url string) bool {
	for _, row := range rows {
		if row.URL == url {
			return true
		}
	}
	return false
}

func (engine *Engine) roundTool(ctx context.Context, task *Task, parsed *researchCreateInput, action roundAction, opSeed []any) (string, bool) {
	if parsed.mcp == nil {
		return "本次研究未选择外部工具", false
	}
	for _, tool := range parsed.mcp.Tools {
		if tool.ServerID+":"+tool.Name != strings.TrimSpace(action.Tool) {
			continue
		}
		if action.Arguments == nil {
			action.Arguments = map[string]any{}
		}
		if err := engine.callMcpTool(ctx, task, parsed.mcp, tool, action.Arguments, opSeed); err != nil {
			return "外部工具 " + tool.Name + " 调用失败：" + err.Error(), false
		}
		return "调用外部工具 " + tool.Name, true
	}
	return "外部工具 " + trimText(action.Tool, 60) + " 不在本次选择中", false
}

func channelLabel(channel string) string {
	if label := externalChannelLabels[channel]; label != "" {
		return label
	}
	return channel
}
