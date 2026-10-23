package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type MediaSortField struct {
	Ascending bool
	Field     string
	Preferred string
}

var sortTokenPattern = regexp.MustCompile(`^(\+)?(res|height|br|tbr|vcodec|acodec)(?::([A-Za-z0-9]+))?$`)

func MediaSort(input string) ([]MediaSortField, error) {
	if input == "" {
		return []MediaSortField{}, nil
	}
	result := []MediaSortField{}
	for _, token := range strings.Split(input, ",") {
		match := sortTokenPattern.FindStringSubmatch(token)
		if match == nil {
			return nil, fmt.Errorf("原生格式排序支持 res、height、br、tbr、vcodec、acodec，可用 + 表示升序")
		}
		if match[3] != "" && match[2] != "vcodec" && match[2] != "acodec" {
			return nil, fmt.Errorf("数值排序不使用额外参数，请通过画质或音质上限设置")
		}
		result = append(result, MediaSortField{Ascending: match[1] == "+", Field: match[2], Preferred: match[3]})
	}
	return result, nil
}

type NativeMediaStream struct {
	ID        string
	Height    int
	Bandwidth float64
	Codec     string
	Extension string
}

func codecScore(codec string) int {
	switch {
	case strings.HasPrefix(codec, "av01") || strings.HasPrefix(codec, "av1"):
		return 4
	case strings.HasPrefix(codec, "vp"):
		return 3
	case strings.HasPrefix(codec, "hev") || strings.HasPrefix(codec, "hvc"):
		return 2
	case strings.HasPrefix(codec, "avc") || strings.HasPrefix(codec, "h264"):
		return 1
	}
	return 0
}

func freeStream(row NativeMediaStream) bool {
	return row.Extension == "webm" || strings.HasPrefix(row.Codec, "vp") ||
		strings.HasPrefix(row.Codec, "av01") || strings.HasPrefix(row.Codec, "opus") ||
		strings.HasPrefix(row.Codec, "vorbis")
}

func SortMediaStreams(rows []NativeMediaStream, input string, freeFormats bool) []NativeMediaStream {
	fields, _ := MediaSort(input)
	sorted := append([]NativeMediaStream{}, rows...)
	codecStarts := func(codec, preferred string) bool {
		if preferred == "" {
			return false
		}
		return strings.HasPrefix(codec, preferred)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		for _, field := range fields {
			var av, bv float64
			if field.Field == "res" || field.Field == "height" {
				av, bv = float64(a.Height), float64(b.Height)
			} else if field.Field == "br" || field.Field == "tbr" {
				av, bv = a.Bandwidth, b.Bandwidth
			} else {
				preferred := field.Preferred
				if preferred == "h264" {
					preferred = "avc"
				} else if preferred == "h265" {
					preferred = "hev"
				} else if preferred == "av1" {
					preferred = "av01"
				}
				if preferred != "" {
					if codecStarts(a.Codec, preferred) {
						av = 1
					}
					if codecStarts(b.Codec, preferred) {
						bv = 1
					}
				} else {
					av = float64(codecScore(a.Codec))
					bv = float64(codecScore(b.Codec))
				}
			}
			if av != bv {
				if field.Ascending {
					return av < bv
				}
				return av > bv
			}
		}
		if freeFormats {
			freeA, freeB := freeStream(a), freeStream(b)
			if freeA != freeB {
				return freeB
			}
		}
		if b.Height != a.Height {
			return b.Height < a.Height
		}
		if b.Bandwidth != a.Bandwidth {
			return b.Bandwidth < a.Bandwidth
		}
		return a.ID < b.ID
	})
	return sorted
}

var proxySchemePattern = regexp.MustCompile(`^(https?|socks5h?):$`)

func ComputeHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func validateProxy(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("代理地址无效")
	}
	if !proxySchemePattern.MatchString(parsed.Scheme) {
		return fmt.Errorf("代理需为不含密码的 HTTP/SOCKS 地址")
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("代理需为不含密码的 HTTP/SOCKS 地址")
	}
	return nil
}
