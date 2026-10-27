package research

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Skill struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Body        string     `json:"body"`
	Enabled     bool       `json:"enabled"`
	Resources   []Resource `json:"resources,omitempty"`
}

type Resource struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Instructions struct {
	Enabled bool   `json:"enabled"`
	Body    string `json:"body"`
}

type extensionCatalog struct {
	Revision             int          `json:"revision"`
	Skills               []Skill      `json:"skills"`
	StandingInstructions Instructions `json:"standing_instructions"`
	DisabledPlugins      []string     `json:"disabled_plugins"`
}

type Plugin struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Channels    []string `json:"channels"`
}

var extensionPlugins = []Plugin{
	{ID: "library", Name: "资料库阅读与管理", Description: "阅读原生知识库中的版本与引用原文。", Channels: []string{"library"}},
	{ID: "papers", Name: "论文检索与阅读", Description: "按明确选定渠道检索题录、摘要和全文。", Channels: []string{"arxiv", "crossref", "semantic", "arxiv_fulltext"}},
	{ID: "web", Name: "网页搜索与阅读", Description: "后台搜索公开网页并保留范围回执。", Channels: []string{"web", "web_pages"}},
	{ID: "memory", Name: "学习记忆", Description: "仅召回用户保存且可用的记忆。", Channels: []string{"memory"}},
	{ID: "notes", Name: "笔记检索", Description: "检索用户明确保存的笔记。", Channels: []string{"notes"}},
	{ID: "conversations", Name: "历史问答", Description: "读取选定知识会话，历史回答不作为原文。", Channels: []string{"conversations"}},
	{ID: "practice", Name: "练习题查找", Description: "检索题干，不读取答案或成绩。", Channels: []string{"practice"}},
	{ID: "organize", Name: "分类建议", Description: "准备分类草稿，由用户确认保存。", Channels: []string{"organize"}},
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var skillFrontMatter = regexp.MustCompile(`(?s)^\x{FEFF}?---\r?\n(.*?)\r?\n---\r?\n(.+)$`)

func validSkillText(value string, limit int) bool {
	if len([]rune(value)) > limit {
		return false
	}
	for _, char := range value {
		if char <= 8 || char == 11 || char == 12 || (char >= 14 && char <= 31) {
			return false
		}
	}
	return true
}

func (store *Store) extensionCatalog() (extensionCatalog, error) {
	if row, err := store.Read("extensions", "catalog"); err == nil {
		encoded, _ := json.Marshal(row)
		var catalog extensionCatalog
		if json.Unmarshal(encoded, &catalog) == nil {
			if catalog.Skills == nil {
				catalog.Skills = []Skill{}
			}
			if catalog.DisabledPlugins == nil {
				catalog.DisabledPlugins = []string{}
			}
			return catalog, nil
		}
	}
	return extensionCatalog{Skills: []Skill{}, StandingInstructions: Instructions{}, DisabledPlugins: []string{}}, nil
}

func (store *Store) Extensions() (map[string]any, error) {
	catalog, err := store.extensionCatalog()
	if err != nil {
		return nil, err
	}
	plugins := []any{}
	for _, plugin := range extensionPlugins {
		enabled := true
		for _, disabled := range catalog.DisabledPlugins {
			if disabled == plugin.ID {
				enabled = false
				break
			}
		}
		plugins = append(plugins, map[string]any{
			"id": plugin.ID, "name": plugin.Name, "description": plugin.Description,
			"channels": plugin.Channels, "enabled": enabled,
		})
	}
	return map[string]any{
		"revision": catalog.Revision, "skills": catalog.Skills,
		"standing_instructions": catalog.StandingInstructions, "plugins": plugins,
	}, nil
}

func (store *Store) ChangeExtensions(input map[string]any) (any, error) {
	op := text(input["op"])
	if !opPattern.MatchString(op) {
		return nil, fmt.Errorf("操作编号无效")
	}
	return store.Operation(op, input, func() (any, error) {
		catalog, err := store.extensionCatalog()
		if err != nil {
			return nil, err
		}
		if catalog.Revision != int(number(input["revision"])) {
			return nil, fmt.Errorf("学习方法版本已改变，请刷新")
		}
		change, _ := input["change"].(map[string]any)
		if change == nil {
			return nil, fmt.Errorf("学习方法变更无效")
		}
		kind := text(change["kind"])
		switch kind {
		case "instructions":
			body := text(change["body"])
			enabled := change["enabled"] == true
			if !validSkillText(body, 4000) || (enabled && strings.TrimSpace(body) == "") {
				return nil, fmt.Errorf("启用的固定要求不能为空")
			}
			catalog.StandingInstructions = Instructions{Enabled: enabled, Body: strings.TrimSpace(body)}
		case "plugin":
			id := text(change["id"])
			found := false
			for _, plugin := range extensionPlugins {
				if plugin.ID == id {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("未知工具组")
			}
			next := []string{}
			for _, value := range catalog.DisabledPlugins {
				if value != id {
					next = append(next, value)
				}
			}
			if change["enabled"] != true {
				next = append(next, id)
			}
			catalog.DisabledPlugins = uniqueStrings(next)
		case "remove":
			id := text(change["id"])
			found := false
			skills := []Skill{}
			for _, skill := range catalog.Skills {
				if skill.ID == id {
					found = true
					continue
				}
				skills = append(skills, skill)
			}
			if !found {
				return nil, fmt.Errorf("学习方法不存在")
			}
			catalog.Skills = skills
		case "skill":
			id := text(change["id"])
			name := text(change["name"])
			description := text(change["description"])
			body := text(change["body"])
			if id != "" {
				found := false
				for _, skill := range catalog.Skills {
					if skill.ID == id {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("学习方法不存在")
				}
			}
			if !skillNamePattern.MatchString(name) || len([]rune(name)) > 64 ||
				!validSkillText(description, 500) || strings.TrimSpace(description) == "" ||
				!validSkillText(body, 6000) || strings.TrimSpace(body) == "" {
				return nil, fmt.Errorf("学习方法内容无效")
			}
			for _, skill := range catalog.Skills {
				if skill.Name == name && skill.ID != id {
					return nil, fmt.Errorf("学习方法名称已存在")
				}
			}
			if id == "" {
				id = randomID()
				if len(catalog.Skills) >= 50 {
					return nil, fmt.Errorf("最多保存50个学习方法")
				}
			}
			var previous *Skill
			skills := []Skill{}
			for _, skill := range catalog.Skills {
				if skill.ID == id {
					copied := skill
					previous = &copied
					continue
				}
				skills = append(skills, skill)
			}
			next := Skill{ID: id, Name: name, Description: description, Body: body, Enabled: change["enabled"] == true}
			if raw, exists := change["resources"]; exists {
				items, _ := raw.([]any)
				if len(items) > 12 {
					return nil, fmt.Errorf("方法资料重复或过长")
				}
				seen := map[string]bool{}
				total := 0
				for _, item := range items {
					row, _ := item.(map[string]any)
					path := text(row["path"])
					content := text(row["content"])
					if path == "" || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\:") ||
						len([]rune(path)) > 200 || strings.Contains(path, "../") || !validSkillText(content, 6000) ||
						strings.TrimSpace(content) == "" {
						return nil, fmt.Errorf("方法资料路径无效")
					}
					if seen[strings.ToLower(path)] {
						return nil, fmt.Errorf("方法资料重复或过长")
					}
					seen[strings.ToLower(path)] = true
					total += len([]rune(content))
					next.Resources = append(next.Resources, Resource{Path: path, Content: content})
				}
				if total > 24000 {
					return nil, fmt.Errorf("方法资料重复或过长")
				}
			} else if previous != nil {
				next.Resources = previous.Resources
			}
			catalog.Skills = append(skills, next)
		default:
			return nil, fmt.Errorf("学习方法变更无效")
		}
		catalog.Revision++
		encoded, _ := json.Marshal(catalog)
		var value map[string]any
		_ = json.Unmarshal(encoded, &value)
		if err := store.Put("extensions", "catalog", catalog.Revision, value); err != nil {
			return nil, err
		}
		return store.Extensions()
	})
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func ParseSkillMarkdown(input string) (map[string]any, error) {
	if len([]rune(input)) > 12000 || !validSkillText(input, 12000) {
		return nil, fmt.Errorf("方法文件超过 12000 字，请精简后导入")
	}
	match := skillFrontMatter.FindStringSubmatch(input)
	if match == nil {
		return nil, fmt.Errorf("方法文件需包含 name 和 description 的头部及正文")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(match[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		index := strings.Index(line, ":")
		if index <= 0 {
			return nil, fmt.Errorf("方法文件头部格式不支持")
		}
		key := strings.TrimSpace(line[:index])
		if key != "name" && key != "description" {
			return nil, fmt.Errorf("方法文件头部格式不支持")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("方法文件头部格式不支持")
		}
		value := strings.TrimSpace(line[index+1:])
		if strings.HasPrefix(value, `"`) {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) != nil {
				return nil, fmt.Errorf("方法头部引号无效")
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		} else if strings.HasPrefix(value, "!") || strings.HasPrefix(value, "&") ||
			strings.HasPrefix(value, "*") || strings.HasPrefix(value, "[") ||
			strings.HasPrefix(value, "{") || strings.HasPrefix(value, ">") ||
			strings.HasPrefix(value, "|") {
			return nil, fmt.Errorf("方法头部仅支持普通文本")
		}
		fields[key] = value
	}
	name := fields["name"]
	description := fields["description"]
	body := strings.TrimSpace(match[2])
	if !skillNamePattern.MatchString(name) || len([]rune(name)) > 64 ||
		strings.TrimSpace(description) == "" || len([]rune(description)) > 500 ||
		body == "" || len([]rune(body)) > 6000 {
		return nil, fmt.Errorf("方法文件头部格式不支持")
	}
	return map[string]any{"name": name, "description": description, "body": body, "enabled": false}, nil
}

type SkillSelection struct {
	Revision int      `json:"revision"`
	IDs      []string `json:"ids"`
}

type ExtensionSnapshot struct {
	Revision     int      `json:"revision"`
	Skills       []Skill  `json:"skills"`
	Instructions string   `json:"instructions"`
	Channels     []string `json:"channels"`
}

func (store *Store) SnapshotExtensions(selection *SkillSelection, channels []string) (ExtensionSnapshot, error) {
	catalog, err := store.extensionCatalog()
	if err != nil {
		return ExtensionSnapshot{}, err
	}
	if selection != nil && selection.Revision != catalog.Revision {
		return ExtensionSnapshot{}, fmt.Errorf("学习方法已改变，请重新选择")
	}
	ids := []string{}
	if selection != nil {
		ids = selection.IDs
	}
	seen := map[string]bool{}
	skills := []Skill{}
	total := 0
	for _, id := range ids {
		if seen[id] {
			return ExtensionSnapshot{}, fmt.Errorf("所选学习方法重复")
		}
		seen[id] = true
		found := false
		for _, skill := range catalog.Skills {
			if skill.ID == id && skill.Enabled {
				found = true
				skills = append(skills, skill)
				total += len([]rune(skill.Body))
				for _, resource := range skill.Resources {
					total += len([]rune(resource.Content))
				}
				break
			}
		}
		if !found {
			return ExtensionSnapshot{}, fmt.Errorf("所选学习方法已停用或删除")
		}
	}
	if len(skills) > 5 || total > 36000 {
		return ExtensionSnapshot{}, fmt.Errorf("本轮最多选择5个方法，含附属资料合计不超过36000字")
	}
	for _, channel := range channels {
		group := ""
		for _, plugin := range extensionPlugins {
			for _, item := range plugin.Channels {
				if item == channel {
					group = plugin.ID
					break
				}
			}
			if group != "" {
				break
			}
		}
		if group == "" {
			return ExtensionSnapshot{}, fmt.Errorf("本次所需工具组已停用")
		}
		for _, disabled := range catalog.DisabledPlugins {
			if disabled == group {
				return ExtensionSnapshot{}, fmt.Errorf("本次所需工具组已停用")
			}
		}
	}
	if contains(channels, "organize") {
		for _, disabled := range catalog.DisabledPlugins {
			if disabled == "library" {
				return ExtensionSnapshot{}, fmt.Errorf("分类建议需启用资料库阅读")
			}
		}
	}
	instructions := ""
	if catalog.StandingInstructions.Enabled {
		instructions = catalog.StandingInstructions.Body
	}
	return ExtensionSnapshot{
		Revision: catalog.Revision, Skills: skills, Instructions: instructions, Channels: channels,
	}, nil
}

func (store *Store) VerifyExtensions(snapshot ExtensionSnapshot) error {
	selection := &SkillSelection{Revision: snapshot.Revision, IDs: []string{}}
	for _, skill := range snapshot.Skills {
		selection.IDs = append(selection.IDs, skill.ID)
	}
	current, err := store.SnapshotExtensions(selection, snapshot.Channels)
	if err != nil {
		return fmt.Errorf("研究期间学习要求或方法已改变，旧结果未发布")
	}
	left, _ := json.Marshal(current)
	right, _ := json.Marshal(snapshot)
	if string(left) != string(right) {
		return fmt.Errorf("研究期间学习要求或方法已改变，旧结果未发布")
	}
	return nil
}
