// Package summaryinput is the pure subtitle-summary chunk and wire-input contract.
package summaryinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

const ChunkLimit = 15000
const ChunkPolicy = "subtitle-text-runes"

type Cue struct {
	Cue  int    `json:"cue"`
	Time string `json:"time"`
	Text string `json:"text"`
}
type Input struct {
	Mode string `json:"mode"`
	Cues []Cue  `json:"cues"`
}

func Chunks(segments []documents.Segment, limit int) [][]documents.Segment {
	chunks := [][]documents.Segment{}
	length := 0
	for _, segment := range segments {
		size := len([]rune(segment.Text))
		if len(chunks) == 0 || length+size > limit {
			chunks = append(chunks, []documents.Segment{segment})
			length = size
			continue
		}
		chunks[len(chunks)-1] = append(chunks[len(chunks)-1], segment)
		length += size
	}
	return chunks
}
func Cues(part []documents.Segment, start int) []Cue {
	cues := make([]Cue, 0, len(part))
	for offset, segment := range part {
		ms := segment.Start
		if ms < 0 {
			ms = 0
		}
		seconds := ms / 1000
		cues = append(cues, Cue{Cue: start + offset, Time: fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds/60)%60, seconds%60), Text: segment.Text})
	}
	return cues
}
func New(mode string, part []documents.Segment, start int) Input {
	return Input{Mode: mode, Cues: Cues(part, start)}
}
func Encode(input Input) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(input); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
func Digest(input Input) (string, error) {
	body, err := Encode(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
