package ai

import (
	"errors"
	"fmt"
	"io"
)

// These are wire-byte limits, not token estimates or provider billing limits.
// Apply before decoding, including reasoning text and SSE framing.
const maxChatResponseBytes int64 = 8 << 20
const maxResponseJSONDepth = 64
const maxResponseJSONStringBytes = 1 << 20

var errChatResponseLimit = errors.New("API 响应超过字节上限，未保存不完整结果")

func publicResponseReadError(err error) error {
	if errors.Is(err, errChatResponseLimit) {
		return errChatResponseLimit
	}
	return fmt.Errorf("API 响应读取失败，请稍后重试")
}

type responseLimitReader struct {
	reader    io.Reader
	remaining int64
}

func (reader *responseLimitReader) Read(buffer []byte) (int, error) {
	if int64(len(buffer)) > reader.remaining+1 {
		buffer = buffer[:reader.remaining+1]
	}
	n, err := reader.reader.Read(buffer)
	if int64(n) > reader.remaining {
		return 0, errChatResponseLimit
	}
	reader.remaining -= int64(n)
	return n, err
}

func boundedChatResponse(reader io.Reader) io.Reader {
	return &responseLimitReader{reader: reader, remaining: maxChatResponseBytes}
}

// Scan bytes before JSON allocation. json.Unmarshal remains responsible for
// syntax; this pass only rejects excessive nesting and individual strings.
func validateResponseJSONBounds(body []byte) error {
	depth, stringBytes := 0, 0
	quoted, escaped := false, false
	for _, value := range body {
		if quoted {
			if !escaped && value == '"' {
				quoted = false
				continue
			}
			stringBytes++
			if stringBytes > maxResponseJSONStringBytes {
				return fmt.Errorf("API 响应字符串超过上限")
			}
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
			}
			continue
		}
		switch value {
		case '"':
			quoted, stringBytes = true, 0
		case '{', '[':
			depth++
			if depth > maxResponseJSONDepth {
				return fmt.Errorf("API 响应嵌套超过上限")
			}
		case '}', ']':
			depth--
		}
	}
	return nil
}
