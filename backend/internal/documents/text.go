package documents

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

const maxDocxXMLBytes = 64 << 20

var wbrPattern = regexp.MustCompile(`(?i)<w:br\s*/?\s*>`)
var wtabPattern = regexp.MustCompile(`(?i)<w:tab\s*/?\s*>`)
var wdelTextPattern = regexp.MustCompile(`(?is)<w:delText[^>]*>.*?</w:delText>`)
var winstrTextPattern = regexp.MustCompile(`(?is)<w:instrText[^>]*>.*?</w:instrText>`)
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
	if documentFile.UncompressedSize64 > maxDocxXMLBytes {
		return "", fmt.Errorf("Word 原文文件过大")
	}
	reader, err := documentFile.Open()
	if err != nil {
		return "", fmt.Errorf("Word 原文文件损坏")
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, maxDocxXMLBytes+1))
	if err != nil {
		return "", fmt.Errorf("Word 原文文件损坏")
	}
	if len(raw) > maxDocxXMLBytes {
		return "", fmt.Errorf("Word 原文文件过大")
	}
	xml := string(raw)
	xml = wdelTextPattern.ReplaceAllString(xml, "")
	xml = winstrTextPattern.ReplaceAllString(xml, "")
	xml = wbrPattern.ReplaceAllString(xml, "\n")
	xml = wtabPattern.ReplaceAllString(xml, " ")
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

// encodingSampleBytes 是编码检测的采样长度；整份文件跑 chardet 既慢又占内存。
const encodingSampleBytes = 64 << 10

// ErrTextEncoding 表示文本编码无法可靠识别。
var ErrTextEncoding = errors.New("无法识别文本编码，请另存为 UTF-8 后再导入")

var (
	utf16LE = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	utf16BE = unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
)

// DecodeTextFile 尽量把文本文件解码为 UTF-8；无法可靠识别编码时退回原始字节，供不需要逐文件报错的调用方使用。
func DecodeTextFile(data []byte) string {
	text, err := DecodeText(data)
	if err != nil {
		return string(data)
	}
	return text
}

// DecodeText 把文本文件解码为 UTF-8：识别 UTF-8/UTF-16（BOM、chardet 或零字节分布）、Big5、GB18030 和 Windows-1252；
// 其余编码或解码后出现替换字符时返回错误，而不是把乱码写进资料。
func DecodeText(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE {
			return decodeStrict(utf16LE, data[2:])
		}
		if data[0] == 0xFE && data[1] == 0xFF {
			return decodeStrict(utf16BE, data[2:])
		}
	}
	if utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 {
		return string(data), nil
	}
	encodingName := ""
	if result, err := chardet.NewTextDetector().DetectBest(encodingSample(data)); err == nil {
		encodingName = strings.ToUpper(result.Charset)
	}
	codec := textCodec(encodingName, data)
	if codec == nil {
		if encodingName == "" {
			return "", ErrTextEncoding
		}
		return "", fmt.Errorf("%w（检测到 %s）", ErrTextEncoding, encodingName)
	}
	return decodeStrict(codec, data)
}

func textCodec(encodingName string, data []byte) encoding.Encoding {
	switch {
	case strings.Contains(encodingName, "UTF-16LE"):
		return utf16LE
	case strings.Contains(encodingName, "UTF-16BE"):
		return utf16BE
	}
	if codec := utf16WithoutBOM(data); codec != nil {
		return codec
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	switch {
	case strings.Contains(encodingName, "BIG5"):
		return traditionalchinese.Big5
	case strings.Contains(encodingName, "GB"):
		return simplifiedchinese.GB18030
	case strings.Contains(encodingName, "WINDOWS-1252"), strings.Contains(encodingName, "ISO-8859"):
		return charmap.Windows1252
	}
	return nil
}

func encodingSample(data []byte) []byte {
	if len(data) <= encodingSampleBytes {
		return data
	}
	return data[:encodingSampleBytes]
}

// utf16WithoutBOM 用零字节分布识别无 BOM 的 UTF-16：以 ASCII 为主的 UTF-16 文本每两个字节就有一个零。
func utf16WithoutBOM(data []byte) encoding.Encoding {
	head := encodingSample(data)
	if len(data)%2 != 0 || len(head) < 4 {
		return nil
	}
	zeros := [2]int{}
	for index, value := range head {
		if value == 0 {
			zeros[index%2]++
		}
	}
	pairs := len(head) / 2
	switch {
	case zeros[1]*10 >= pairs*3 && zeros[0]*10 < pairs:
		return utf16LE
	case zeros[0]*10 >= pairs*3 && zeros[1]*10 < pairs:
		return utf16BE
	}
	return nil
}

func decodeStrict(codec encoding.Encoding, data []byte) (string, error) {
	decoded, err := codec.NewDecoder().Bytes(data)
	if err != nil || !utf8.Valid(decoded) || bytes.ContainsRune(decoded, utf8.RuneError) {
		return "", ErrTextEncoding
	}
	return string(decoded), nil
}

func decodeTextWith(encoding encoding.Encoding, data []byte) string {
	decoded, err := encoding.NewDecoder().Bytes(data)
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
