package documents

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

type coursewareImage struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type CoursewareResult struct {
	Text      string            `json:"text"`
	NeedsOcr  bool              `json:"needsOcr"`
	Images    []coursewareImage `json:"images"`
	Omissions []string          `json:"omissions"`
}

var (
	slideIDPattern    = regexp.MustCompile(`<p:sldId\b[^>]*\br:id=["']([^"']+)["'][^>]*\/?\s*>`)
	relationshipPat   = regexp.MustCompile(`<Relationship\b[^>]*\/?\s*>`)
	attrPattern       = func(name string) *regexp.Regexp { return regexp.MustCompile(`\b` + name + `=["']([^"']+)["']`) }
	textRunPattern    = regexp.MustCompile(`(?s)<a:t(?:\s[^>]*)?>([\s\S]*?)</a:t>`)
	blipPattern       = regexp.MustCompile(`<a:blip\b[^>]*\br:embed=["']([^"']+)["']`)
	embeddedObjectPat = regexp.MustCompile(`<p:graphicFrame\b|<p:oleObj\b|<p:contentPart\b`)
)

func decodeEntities(value string) string {
	value = hexEntityPattern.ReplaceAllStringFunc(value, func(match string) string {
		return decodeEntity(hexEntityPattern.FindStringSubmatch(match), true)
	})
	value = decEntityPattern.ReplaceAllStringFunc(value, func(match string) string {
		return decodeEntity(decEntityPattern.FindStringSubmatch(match), false)
	})
	for _, pair := range [][2]string{
		{"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", "\""}, {"&apos;", "'"}, {"&amp;", "&"},
	} {
		value = strings.ReplaceAll(value, pair[0], pair[1])
	}
	return value
}

func attrValue(xml, name string) string {
	return attrPattern(name).FindStringSubmatch(xml)[1]
}

func partTarget(parent, value string) string {
	target := decodeEntities(value)
	if strings.HasPrefix(target, "/") {
		return path.Clean(target[1:])
	}
	return path.Clean(path.Join(parent, target))
}

var mimeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif",
}

func readZipEntry(archive *zip.Reader, name string, limit int64, label string) (string, error) {
	entry := findEntry(archive, name)
	if entry == nil {
		return "", fmt.Errorf("%s 内容缺失", label)
	}
	reader, err := entry.Open()
	if err != nil {
		return "", fmt.Errorf("%s 展开失败", label)
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return "", fmt.Errorf("%s 展开失败", label)
	}
	if int64(len(raw)) > limit {
		return "", fmt.Errorf("%s超过 %d 字节本地解析上限", label, limit)
	}
	return string(raw), nil
}

func findEntry(archive *zip.Reader, name string) *zip.File {
	for _, file := range archive.File {
		if file.Name == name {
			return file
		}
	}
	return nil
}

func ParsePptx(data []byte) (CoursewareResult, error) {
	result := CoursewareResult{Images: []coursewareImage{}, Omissions: []string{}}
	if int64(len(data)) > 200*1024*1024 {
		return result, fmt.Errorf("PPTX 文件超过 200 MB 本地读取上限")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return result, fmt.Errorf("PPTX 文件无法打开")
	}
	presentationXML, err := readZipEntry(archive, "ppt/presentation.xml", 5*1024*1024, "PPTX 页面目录")
	if err != nil {
		return result, err
	}
	presentationRels, err := readZipEntry(archive, "ppt/_rels/presentation.xml.rels", 5*1024*1024, "PPTX 页面关系")
	if err != nil {
		return result, err
	}
	var slideNames []string
	for _, match := range slideIDPattern.FindAllStringSubmatch(presentationXML, -1) {
		id := match[1]
		var relationship string
		for _, row := range relationshipPat.FindAllString(presentationRels, -1) {
			if attrPattern("Id").FindStringSubmatch(row)[1] == id {
				relationship = row
				break
			}
		}
		target := ""
		if relationship != "" {
			matches := attrPattern("Target").FindStringSubmatch(relationship)
			if len(matches) > 1 {
				target = matches[1]
			}
		}
		if relationship == "" || target == "" {
			return result, fmt.Errorf("PPTX 页面引用无效")
		}
		name := partTarget("ppt", target)
		if !strings.HasPrefix(name, "ppt/slides/") || strings.Contains(name, "../") {
			return result, fmt.Errorf("PPTX 页面路径无效")
		}
		slideNames = append(slideNames, name)
	}
	if len(slideNames) == 0 || len(slideNames) > 500 {
		return result, fmt.Errorf("PPTX 页面数须在 1–500 页之间")
	}
	editableChars := 0
	for index, slideName := range slideNames {
		slideXML, err := readZipEntry(archive, slideName, 5*1024*1024, fmt.Sprintf("PPTX 第 %d 页内容", index+1))
		if err != nil {
			return result, err
		}
		var parts []string
		for _, match := range textRunPattern.FindAllStringSubmatch(slideXML, -1) {
			parts = append(parts, decodeEntities(match[1]))
		}
		extracted := strings.TrimSpace(strings.Join(parts, "\n"))
		remaining := 200000 - editableChars
		if remaining < 0 {
			remaining = 0
		}
		text := extracted
		if len(text) > remaining {
			text = text[:remaining]
		}
		editableChars += len(text)
		if len(text) < len(extracted) {
			result.Omissions = append(result.Omissions, "课件可编辑文本超过 200,000 字，本地解析仅保留前段内容")
		}
		pageText := text
		if pageText == "" {
			pageText = "（未提取到可编辑文字）"
		}
		result.Text += fmt.Sprintf("\n[第 %d 页]\n%s", index+1, pageText)
		relsName := path.Join(path.Dir(slideName), "_rels", path.Base(slideName)+".rels")
		slideRel, err := readZipEntry(archive, relsName, 5*1024*1024, fmt.Sprintf("PPTX 第 %d 页关系", index+1))
		if err != nil {
			continue
		}
		collectSlideImages(archive, slideXML, slideRel, slideName, index, &result)
		if embeddedObjectPat.MatchString(slideXML) {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页含图表、SmartArt 或嵌入对象；仅提取其中的可编辑文本，未进行完整页面渲染", index+1))
		}
	}
	result.NeedsOcr = false
	return result, nil
}

func collectSlideImages(archive *zip.Reader, slideXML, slideRel, slideName string, index int, result *CoursewareResult) {
	seen := map[string]bool{}
	var imageBudget int64 = 8 * 1024 * 1024
	for _, match := range blipPattern.FindAllStringSubmatch(slideXML, -1) {
		id := match[1]
		var relationship string
		for _, row := range relationshipPat.FindAllString(slideRel, -1) {
			if attrPattern("Id").FindStringSubmatch(row)[1] == id {
				relationship = row
				break
			}
		}
		if relationship == "" {
			continue
		}
		matches := attrPattern("Target").FindStringSubmatch(relationship)
		if len(matches) < 2 {
			continue
		}
		mediaName := partTarget(path.Dir(slideName), matches[1])
		mime, ok := mimeByExt[strings.ToLower(path.Ext(mediaName))]
		if !strings.HasPrefix(mediaName, "ppt/media/") || strings.Contains(mediaName, "../") || !ok {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页有未支持的嵌入图片格式", index+1))
			continue
		}
		if seen[mediaName] {
			continue
		}
		seen[mediaName] = true
		entry := findEntry(archive, mediaName)
		if entry == nil {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页嵌入图片缺失", index+1))
			continue
		}
		if len(result.Images) >= 40 {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页嵌入图片因 40 张数量上限未发送", index+1))
			continue
		}
		if int64(len(dataOf(entry))) > 4*1024*1024 {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页嵌入图片超过 4 MB 单张上限，未读取或发送", index+1))
			continue
		}
		if imageBudget <= 0 {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页嵌入图片因 8 MB 图片总量上限未读取或发送", index+1))
			continue
		}
		data, err := readEntryBytes(entry, 4*1024*1024)
		if err != nil {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页嵌入图片展开失败", index+1))
			continue
		}
		if int64(len(data)) > imageBudget {
			result.Omissions = append(result.Omissions, fmt.Sprintf("第 %d 页嵌入图片超过剩余图片预算 %d 字节，未读取或发送", index+1, imageBudget))
			continue
		}
		imageBudget -= int64(len(data))
		count := 1
		for _, image := range result.Images {
			if strings.HasPrefix(image.Label, fmt.Sprintf("课件第 %d 页", index+1)) {
				count++
			}
		}
		result.Images = append(result.Images, coursewareImage{
			Label: fmt.Sprintf("课件第 %d 页嵌入图片 %d", index+1, count),
			URL:   "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
		})
	}
}

func dataOf(file *zip.File) []byte {
	reader, err := file.Open()
	if err != nil {
		return nil
	}
	defer reader.Close()
	raw, _ := io.ReadAll(reader)
	return raw
}

func readEntryBytes(file *zip.File, limit int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, limit+1))
}
