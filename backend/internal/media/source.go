package media

type MediaStream struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	URLs      []string `json:"urls"`
	Height    int      `json:"height"`
	Codec     string   `json:"codec"`
	Bandwidth float64  `json:"bandwidth"`
	Extension string   `json:"extension"`
	Transport string   `json:"transport,omitempty"`
}

type SubtitleTrack struct {
	Language  string `json:"language"`
	URL       string `json:"url"`
	Automatic bool   `json:"automatic"`
	Format    string `json:"format,omitempty"`
}

type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

type Catalog struct {
	URL       string              `json:"url"`
	Title     string              `json:"title"`
	Playlist  bool                `json:"playlist"`
	Entries   []map[string]string `json:"entries,omitempty"`
	Limited   bool                `json:"limited,omitempty"`
	Duration  *float64            `json:"duration,omitempty"`
	Heights   []int               `json:"heights,omitempty"`
	Subtitles []string            `json:"subtitles,omitempty"`
	Formats   []map[string]any    `json:"formats,omitempty"`
}

type Source struct {
	Catalog         Catalog           `json:"catalog"`
	Identity        string            `json:"identity"`
	Streams         []MediaStream     `json:"streams"`
	SourceID        string            `json:"sourceId,omitempty"`
	Platform        string            `json:"platform,omitempty"`
	DownloadHeaders map[string]string `json:"downloadHeaders,omitempty"`
	Description     string            `json:"description,omitempty"`
	SubtitleTracks  []SubtitleTrack   `json:"subtitleTracks,omitempty"`
	Thumbnail       string            `json:"thumbnail,omitempty"`
	Chapters        []Chapter         `json:"chapters,omitempty"`
	Bvid            string            `json:"bvid"`
	Cid             int64             `json:"cid"`
	Page            int               `json:"page"`
}
