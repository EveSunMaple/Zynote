package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"html"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

var bvidPattern = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`)
var tagPattern = regexp.MustCompile(`<[^>]*>`)

func urlEncode(value string) string { return url.QueryEscape(value) }

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func isHex(value string) bool {
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}

func sortStrings(values []string) { sort.Strings(values) }

func stripTags(value string) string { return tagPattern.ReplaceAllString(value, "") }

func htmlUnescape(value string) string { return html.UnescapeString(value) }

func clipRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func durationText(value any) string {
	switch typed := value.(type) {
	case float64:
		if typed == 0 {
			return ""
		}
		if typed == math.Trunc(typed) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return typed
	default:
		return ""
	}
}

func allowedQRURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil {
		return false
	}
	return parsed.Scheme == "https" &&
		(parsed.Host == "passport.bilibili.com" || parsed.Host == "account.bilibili.com")
}

func QRImage(value string) (string, error) {
	if len(value) > 2000 {
		return "", contract.InputInvalid("扫码地址无效")
	}
	if !allowedQRURL(value) {
		return "", contract.InputInvalid("扫码地址无效")
	}
	code, err := newQRCode(value)
	if err != nil {
		return "", contract.InputInvalid("扫码地址无效")
	}
	return code, nil
}
