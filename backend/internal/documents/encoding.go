package documents

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// encodingSampleBytes 是编码检测的采样长度；整份文件跑 chardet 既慢又占内存。
const encodingSampleBytes = 64 << 10

// ErrTextEncoding 表示文本编码无法可靠识别。
var ErrTextEncoding = errors.New("无法识别文本编码，请另存为 UTF-8 后再导入")

var (
	utf8BOM    = []byte{0xEF, 0xBB, 0xBF}
	utf16LEBOM = []byte{0xFF, 0xFE}
	utf16BEBOM = []byte{0xFE, 0xFF}
	utf16LE    = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	utf16BE    = unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
)

// DecodeText 把文本文件解码为 UTF-8：识别 UTF-8/UTF-16（带或不带 BOM）、GB18030 和 Big5；
// 其余编码或解码后出现替换字符时返回错误，而不是把乱码写进资料。
func DecodeText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, utf8BOM):
		data = data[len(utf8BOM):]
	case bytes.HasPrefix(data, utf16LEBOM):
		return decodeStrict(utf16LE, data[len(utf16LEBOM):], "UTF-16LE")
	case bytes.HasPrefix(data, utf16BEBOM):
		return decodeStrict(utf16BE, data[len(utf16BEBOM):], "UTF-16BE")
	}
	if utf8.Valid(data) && !bytes.Contains(data, []byte{0}) {
		return string(data), nil
	}
	if order := utf16WithoutBOM(data); order != nil {
		return decodeStrict(order, data, "UTF-16")
	}
	if bytes.Contains(data, []byte{0}) {
		return "", ErrTextEncoding
	}
	charset := ""
	if result, err := chardet.NewTextDetector().DetectBest(sample(data)); err == nil {
		charset = strings.ToUpper(result.Charset)
	}
	for _, candidate := range legacyCandidates(charset) {
		if text, err := decodeStrict(candidate.encoding, data, candidate.name); err == nil {
			return text, nil
		}
	}
	if charset == "" {
		return "", ErrTextEncoding
	}
	return "", fmt.Errorf("%w（检测到 %s）", ErrTextEncoding, charset)
}

type namedEncoding struct {
	name     string
	encoding encoding.Encoding
}

// legacyCandidates 按检测结果给出要尝试的旧编码。拉丁字符集的检测结果对短中文文本并不可靠，
// 因此先试 GB18030（与旧行为一致），失败再按 Windows-1252 解码；日文、韩文等未支持的编码直接拒绝。
func legacyCandidates(charset string) []namedEncoding {
	gb := namedEncoding{"GB18030", simplifiedchinese.GB18030}
	switch {
	case strings.Contains(charset, "GB"):
		return []namedEncoding{gb}
	case strings.Contains(charset, "BIG5"):
		return []namedEncoding{{"Big5", traditionalchinese.Big5}}
	case strings.HasPrefix(charset, "ISO-8859") || strings.HasPrefix(charset, "WINDOWS-125"):
		return []namedEncoding{gb, {"Windows-1252", charmap.Windows1252}}
	}
	return nil
}

func sample(data []byte) []byte {
	if len(data) <= encodingSampleBytes {
		return data
	}
	return data[:encodingSampleBytes]
}

// utf16WithoutBOM 用零字节分布识别无 BOM 的 UTF-16：以 ASCII 为主的 UTF-16 文本每两个字节就有一个零。
func utf16WithoutBOM(data []byte) encoding.Encoding {
	head := sample(data)
	if len(data)%2 != 0 || len(head) < 4 {
		return nil
	}
	evenZero, oddZero := 0, 0
	for index, value := range head {
		if value != 0 {
			continue
		}
		if index%2 == 0 {
			evenZero++
		} else {
			oddZero++
		}
	}
	pairs := len(head) / 2
	switch {
	case oddZero*10 >= pairs*3 && evenZero*10 < pairs:
		return utf16LE
	case evenZero*10 >= pairs*3 && oddZero*10 < pairs:
		return utf16BE
	}
	return nil
}

func decodeStrict(codec encoding.Encoding, data []byte, name string) (string, error) {
	decoded, err := codec.NewDecoder().Bytes(data)
	if err != nil || !utf8.Valid(decoded) || bytes.ContainsRune(decoded, utf8.RuneError) {
		return "", fmt.Errorf("%w（按 %s 解码失败）", ErrTextEncoding, name)
	}
	return string(decoded), nil
}
