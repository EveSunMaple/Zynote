package contract

// PlaybackSourceBinding is journal data, never proof supplied by a caller.
// SavedSubtitle is a canonical library-relative locator and stays in Go journals.
type PlaybackSourceBinding struct {
	Version                int    `json:"version"`
	VideoTaskID            string `json:"videoTaskID"`
	SourceSHA256           string `json:"sourceSHA256"`
	SubtitleSHA256         string `json:"subtitleSHA256"`
	RetentionReceiptSHA256 string `json:"retentionReceiptSHA256"`
	SavedSubtitle          string `json:"savedSubtitle"`
}
