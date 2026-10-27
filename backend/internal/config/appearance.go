package config

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const (
	appearanceFileName         = "appearance.json"
	errAppearanceCourseInvalid = "外观课程标识无效"
)

type Appearance struct {
	Background      string  `json:"background"`
	Image           string  `json:"image"`
	Opacity         float64 `json:"opacity"`
	Blur            float64 `json:"blur"`
	Scrim           float64 `json:"scrim"`
	Accent          string  `json:"accent"`
	FontSize        float64 `json:"fontSize"`
	LineHeight      float64 `json:"lineHeight"`
	AvatarSize      float64 `json:"avatarSize"`
	AvatarShape     string  `json:"avatarShape"`
	ShowNames       bool    `json:"showNames"`
	AssistantBubble bool    `json:"assistantBubble"`
	BubbleRadius    float64 `json:"bubbleRadius"`
	MessageGap      float64 `json:"messageGap"`
	ContentWidth    float64 `json:"contentWidth"`
}

type AppearanceState struct {
	Global  Appearance            `json:"global"`
	Courses map[string]Appearance `json:"courses"`
}

var DefaultAppearance = Appearance{
	Background:      "none",
	Image:           "",
	Opacity:         0.7,
	Blur:            2,
	Scrim:           0.5,
	Accent:          "default",
	FontSize:        16,
	LineHeight:      1.65,
	AvatarSize:      40,
	AvatarShape:     "round",
	ShowNames:       true,
	AssistantBubble: false,
	BubbleRadius:    18,
	MessageGap:      28,
	ContentWidth:    832,
}

var backgrounds = map[string]bool{"none": true, "lavender": true, "rose": true, "mint": true, "image": true}
var accents = map[string]bool{"default": true, "lavender": true, "rose": true, "mint": true, "blue": true}

func ReadAppearance(dataRoot string) (AppearanceState, error) {
	path := filepath.Join(dataRoot, appearanceFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AppearanceState{Global: DefaultAppearance, Courses: map[string]Appearance{}}, nil
		}
		return AppearanceState{}, contract.Unavailable("外观文件不可读")
	}
	var state AppearanceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return AppearanceState{}, contract.InputInvalid("外观文件格式无效")
	}
	if err := validateAppearance(state.Global); err != nil {
		return AppearanceState{}, err
	}
	for courseID, value := range state.Courses {
		if !IsUUID(courseID) {
			return AppearanceState{}, contract.InputInvalid(errAppearanceCourseInvalid)
		}
		if err := validateAppearance(value); err != nil {
			return AppearanceState{}, err
		}
	}
	if state.Courses == nil {
		state.Courses = map[string]Appearance{}
	}
	return state, nil
}

func validateAppearance(value Appearance) error {
	if !backgrounds[value.Background] || !accents[value.Accent] ||
		(value.AvatarShape != "round" && value.AvatarShape != "rounded") ||
		outOfRange(value.Opacity, 0, 1) || outOfRange(value.Blur, 0, 24) ||
		outOfRange(value.Scrim, 0, 1) || outOfRange(value.FontSize, 12, 24) ||
		outOfRange(value.LineHeight, 1.3, 2.2) || outOfRange(value.AvatarSize, 28, 72) ||
		outOfRange(value.BubbleRadius, 0, 28) || outOfRange(value.MessageGap, 8, 48) ||
		outOfRange(value.ContentWidth, 560, 1040) ||
		(value.Image != "" && (len(value.Image) > 6000000 ||
			!(strings.HasPrefix(value.Image, "data:image/png;base64,") || strings.HasPrefix(value.Image, "data:image/jpeg;base64,")))) {
		return contract.InputInvalid("外观设置超出允许范围")
	}
	return nil
}

func outOfRange(value, minimum, maximum float64) bool {
	return math.IsNaN(value) || value < minimum || value > maximum
}

func SaveAppearance(dataRoot string, raw json.RawMessage, courseID *string) (AppearanceState, error) {
	var value Appearance
	if err := json.Unmarshal(raw, &value); err != nil {
		return AppearanceState{}, contract.InputInvalid("外观参数无效")
	}
	if err := validateAppearance(value); err != nil {
		return AppearanceState{}, err
	}
	state, err := ReadAppearance(dataRoot)
	if err != nil {
		return AppearanceState{}, err
	}
	if courseID != nil {
		if !IsUUID(*courseID) {
			return AppearanceState{}, contract.InputInvalid(errAppearanceCourseInvalid)
		}
		state.Courses[*courseID] = value
	} else {
		state.Global = value
	}
	if err := writeAtomic(filepath.Join(dataRoot, appearanceFileName), state); err != nil {
		return AppearanceState{}, contract.Unavailable("外观文件不可写")
	}
	return state, nil
}

func ClearAppearance(dataRoot, courseID string) (AppearanceState, error) {
	if !IsUUID(courseID) {
		return AppearanceState{}, contract.InputInvalid(errAppearanceCourseInvalid)
	}
	state, err := ReadAppearance(dataRoot)
	if err != nil {
		return AppearanceState{}, err
	}
	delete(state.Courses, courseID)
	if err := writeAtomic(filepath.Join(dataRoot, appearanceFileName), state); err != nil {
		return AppearanceState{}, contract.Unavailable("外观文件不可写")
	}
	return state, nil
}
