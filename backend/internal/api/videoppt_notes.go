package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

const msgNotesExportFailed = "笔记导出失败"

type notePoint struct {
	Cue       float64 `json:"cue"`
	Quote     string  `json:"quote"`
	Point     string  `json:"point"`
	Topic     string  `json:"topic"`
	Timestamp string  `json:"timestamp"`
}

type noteGroup struct {
	Title string
	Items []notePoint
}

func noteGroups(points []notePoint) []noteGroup {
	groups := []noteGroup{}
	index := map[string]int{}
	for _, point := range points {
		topic := point.Topic
		if topic == "" {
			topic = "课程内容"
		}
		if position, ok := index[topic]; ok {
			groups[position].Items = append(groups[position].Items, point)
		} else {
			index[topic] = len(groups)
			groups = append(groups, noteGroup{Title: topic, Items: []notePoint{point}})
		}
	}
	return groups
}

func escapeXML(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func notesSVG(title string, points []notePoint) string {
	groups := noteGroups(points)
	height := 80 + len(points)*72 + len(groups)*44
	if height < 240 {
		height = 240
	}
	y := 44
	rows := []string{}
	for _, group := range groups {
		rows = append(rows, fmt.Sprintf(
			`<path d="M180 100H240V%dH280" fill="none" stroke="#c8c1ed" stroke-width="2"/><rect x="280" y="%d" width="640" height="30" rx="8" fill="#eeeafa"/><text x="294" y="%d" font-size="15" font-weight="600">%s</text>`,
			y+15, y, y+21, escapeXML(group.Title),
		))
		y += 44
		for _, point := range group.Items {
			text := []rune(point.Point)
			first := string(text[:minNoteInt(38, len(text))])
			second := ""
			if len(text) > 38 {
				end := minNoteInt(76, len(text))
				second = string(text[38:end])
				if len(text) > 76 {
					second += "…"
				}
			}
			rows = append(rows, fmt.Sprintf(
				`<path d="M290 %dV%dH310" fill="none" stroke="#c8c1ed"/><rect x="310" y="%d" width="610" height="62" rx="10" fill="#f7f6fc"/><text x="324" y="%d" font-size="14">%s</text><text x="324" y="%d" font-size="14">%s</text><text x="830" y="%d" font-size="11" fill="#76718b">%s</text>`,
				y-10, y+20, y, y+21, escapeXML(first), y+40, escapeXML(second), y+53, escapeXML(point.Timestamp),
			))
			y += 72
		}
	}
	titleRunes := []rune(title)
	if len(titleRunes) > 8 {
		titleRunes = titleRunes[:8]
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="960" height="%d" viewBox="0 0 960 %d"><rect width="960" height="%d" fill="#fff"/><g font-family="Microsoft YaHei,sans-serif" fill="#282534"><rect x="20" y="66" width="160" height="68" rx="14" fill="#e3ddf7"/><text x="34" y="96" font-size="16" font-weight="600">%s</text><text x="34" y="118" font-size="12">%d 项字幕要点</text>%s</g></svg>`,
		height, height, height, escapeXML(string(titleRunes)), len(points), strings.Join(rows, ""),
	)
}

func minNoteInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func docxParagraph(line string) string {
	clean := regexpHeading.ReplaceAllString(line, "")
	clean = strings.ReplaceAll(clean, "**", "")
	bold := strings.HasPrefix(line, "#")
	size := "22"
	if strings.HasPrefix(line, "# ") {
		size = "32"
	}
	properties := `<w:pPr><w:spacing w:after="120"/></w:pPr>`
	runProperties := `<w:sz w:val="` + size + `"/>`
	if bold {
		runProperties = `<w:b/>` + runProperties
	}
	return `<w:p>` + properties + `<w:r><w:rPr>` + runProperties + `</w:rPr><w:t xml:space="preserve">` + escapeXML(clean) + `</w:t></w:r></w:p>`
}

func writeDocxFiles(archive *zip.Writer, files map[string]string) error {
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			return err
		}
	}
	return nil
}

func notesDocx(text string) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	files := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
	}
	paragraphs := []string{}
	for _, line := range strings.Split(text, "\n") {
		paragraphs = append(paragraphs, docxParagraph(line))
	}
	files["word/document.xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + strings.Join(paragraphs, "") + `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134"/></w:sectPr></w:body></w:document>`
	if err := writeDocxFiles(archive, files); err != nil {
		archive.Close()
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

var regexpHeading = regexp.MustCompile(`^#{1,3} `)

func (inv *Invoker) handleVideoNotesExport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Path   string `json:"path"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(params, &input); err != nil ||
		(input.Format != "docx" && input.Format != "pdf" && input.Format != "svg") {
		return nil, contract.InputInvalid("笔记导出参数无效")
	}
	source, err := library.ManagedFile(state.LibraryRoot, input.Path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(strings.ToLower(source), ".md") {
		return nil, contract.InputInvalid("请先生成并保存课程笔记")
	}
	text, err := os.ReadFile(source)
	if err != nil {
		return nil, contract.InputInvalid("笔记文件不可读")
	}
	title := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	directory := filepath.Dir(source)
	path, err := library.ReserveFile(directory, title+"."+input.Format)
	if err != nil {
		return nil, err
	}
	var data []byte
	switch input.Format {
	case "svg":
		evidence := strings.TrimSuffix(source, filepath.Ext(source)) + ".json"
		raw, err := os.ReadFile(evidence)
		if err != nil {
			_ = os.Remove(path)
			return nil, contract.InputInvalid("请先生成并保存课程笔记")
		}
		var points []notePoint
		if json.Unmarshal(raw, &points) != nil || len(points) > 20000 {
			_ = os.Remove(path)
			return nil, contract.InputInvalid("笔记证据数据无效")
		}
		data = []byte(notesSVG(title, points))
	case "docx":
		data, err = notesDocx(string(text))
		if err != nil {
			_ = os.Remove(path)
			return nil, contract.Unavailable(msgNotesExportFailed)
		}
	case "pdf":
		html := `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><style>body{font:12pt 'Microsoft YaHei',sans-serif;line-height:1.7;padding:24px}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit}</style><pre>` + escapeHTML(string(text)) + `</pre>`
		reverse := store.Reverse()
		if reverse == nil {
			_ = os.Remove(path)
			return nil, contract.Unavailable("PDF 导出组件不可用")
		}
		if _, err := reverse.Call("shell.printHtmlPdf", map[string]any{"html": html, "path": path}, 120000); err != nil {
			_ = os.Remove(path)
			return nil, contract.Unavailable(msgNotesExportFailed)
		}
		data, err = os.ReadFile(path)
		if err != nil || len(data) < 5 || !bytes.HasPrefix(data, []byte("%PDF-")) {
			_ = os.Remove(path)
			return nil, contract.Unavailable("笔记导出校验失败")
		}
	}
	if input.Format != "pdf" {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			_ = os.Remove(path)
			return nil, contract.Unavailable(msgNotesExportFailed)
		}
	}
	check, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(check, data) {
		_ = os.Remove(path)
		return nil, contract.Unavailable("笔记导出校验失败")
	}
	return path, nil
}

func escapeHTML(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}
