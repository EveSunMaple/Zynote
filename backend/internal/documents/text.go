package documents

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding/simplifiedchinese"
)

var wbrPattern = regexp.MustCompile(`(?i)<w:br\s*/?\s*>`)
var wpEndPattern = regexp.MustCompile(`(?i)</w:p>`)
var tagPattern = regexp.MustCompile(`<[^>]+>`)
var hexEntityPattern = regexp.MustCompile(`(?i)&#x([\da-f]+);`)
var decEntityPattern = regexp.MustCompile(`&#(\d+);`)

func decodeEntity(match []string, hex bool) string {
	var code int64
	var err error
	if hex {
		code, err = strconv.ParseInt(match[1], 16, 64)
	} else {
		code, err = strconv.ParseInt(match[1], 10, 64)
	}
	if err != nil {
		return ""
	}
	return string(rune(code))
}

func ParseDocx(data []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("Word 原文文件损坏")
	}
	var documentFile *zip.File
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			documentFile = file
			break
		}
	}
	if documentFile == nil {
		return "", fmt.Errorf("Word 原文文件损坏")
	}
	reader, err := documentFile.Open()
	if err != nil {
		return "", fmt.Errorf("Word 原文文件损坏")
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("Word 原文文件损坏")
	}
	xml := string(raw)
	xml = wbrPattern.ReplaceAllString(xml, "\n")
	xml = wpEndPattern.ReplaceAllString(xml, "\n\n")
	xml = tagPattern.ReplaceAllString(xml, "")
	xml = hexEntityPattern.ReplaceAllStringFunc(xml, func(match string) string {
		return decodeEntity(hexEntityPattern.FindStringSubmatch(match), true)
	})
	xml = decEntityPattern.ReplaceAllStringFunc(xml, func(match string) string {
		return decodeEntity(decEntityPattern.FindStringSubmatch(match), false)
	})
	for _, replacement := range [][2]string{
		{"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", "\""}, {"&apos;", "'"}, {"&amp;", "&"},
	} {
		xml = strings.ReplaceAll(xml, replacement[0], replacement[1])
	}
	return xml, nil
}

func DecodeTextFile(data []byte) string {
	detector := chardet.NewTextDetector()
	encoding := "UTF-8"
	if result, err := detector.DetectBest(data); err == nil && result.Charset != "" {
		encoding = strings.ToUpper(result.Charset)
	}
	decoded := string(data)
	if looksBinary(decoded) && (strings.Contains(encoding, "GB") || strings.Contains(encoding, "BIG5") ||
		strings.Contains(encoding, "WINDOWS-1252") || strings.Contains(encoding, "ISO-8859")) {
		decoded = decodeGB(data)
		return decoded
	}
	return decoded
}

func looksBinary(value string) bool {
	return strings.ContainsRune(value, '\uFFFD')
}

func decodeGB(data []byte) string {
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

type ParseResult struct {
	Text     string `json:"text"`
	NeedsOcr bool   `json:"needsOcr"`
}

type NativeCaller interface {
	Call(method string, params any, timeoutMS int) (result json.RawMessage, err error)
}

func ParseDocument(caller NativeCaller, path string) (ParseResult, error) {
	extension := strings.ToLower(filepath.Ext(path))
	data, err := os.ReadFile(path)
	if err != nil {
		return ParseResult{}, fmt.Errorf("原文文件不可读")
	}
	switch extension {
	case ".pdf":
		if caller == nil {
			return ParseResult{}, fmt.Errorf("PDF 解析组件不可用")
		}
		result, err := caller.Call("pdf.plainText", map[string]any{"path": path}, 30000)
		if err != nil {
			return ParseResult{}, err
		}
		var parsed struct {
			Text     string `json:"text"`
			NeedsOcr bool   `json:"needsOcr"`
			NumPages int    `json:"numPages"`
		}
		if err := json.Unmarshal(result, &parsed); err != nil {
			return ParseResult{}, fmt.Errorf("PDF 解析结果无效")
		}
		return ParseResult{Text: parsed.Text, NeedsOcr: parsed.NeedsOcr}, nil
	case ".docx":
		text, err := ParseDocx(data)
		if err != nil {
			return ParseResult{}, err
		}
		return ParseResult{Text: text, NeedsOcr: false}, nil
	case ".txt", ".md":
		return ParseResult{Text: DecodeTextFile(data), NeedsOcr: false}, nil
	}
	return ParseResult{}, fmt.Errorf("资料只支持 PDF、DOCX、TXT 和 Markdown；PPT 请先另存为 PDF")
}

func ParseCourseware(caller NativeCaller, path string) (CoursewareResult, error) {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".ppt" {
		return CoursewareResult{}, fmt.Errorf("旧版 PPT 暂不支持直接解析，请在 PowerPoint 中另存为 PPTX")
	}
	if extension == ".pptx" {
		data, err := os.ReadFile(path)
		if err != nil {
			return CoursewareResult{}, fmt.Errorf("课件文件不可读")
		}
		return ParsePptx(data)
	}
	parsed, err := ParseDocument(caller, path)
	if err != nil {
		return CoursewareResult{}, err
	}
	return CoursewareResult{
		Text:      parsed.Text,
		NeedsOcr:  parsed.NeedsOcr,
		Images:    []CoursewareImage{},
		Omissions: []string{},
	}, nil
}

func ReadTranscript(caller NativeCaller, path string) (SplitResult, error) {
	if strings.ToLower(filepath.Ext(path)) == ".srt" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return SplitResult{}, fmt.Errorf("字幕文件不可读")
		}
		text := DecodeTextFile(raw)
		segments, err := ParseSrt(text)
		if err != nil {
			return SplitResult{}, err
		}
		return SplitResult{Text: text, Segments: segments, Estimated: false}, nil
	}
	parsed, err := ParseDocument(caller, path)
	if err != nil {
		return SplitResult{}, err
	}
	if parsed.NeedsOcr || strings.TrimSpace(parsed.Text) == "" {
		return SplitResult{}, fmt.Errorf("原文没有可读取的文字，请导入智云导出的 Word 或文本原文")
	}
	return SplitTranscript(parsed.Text)
}
