package videoppt

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type PreparedSubtitle struct {
	Version     int    `json:"version"`
	VideoSHA256 string `json:"videoSHA256"`
	SRT         string `json:"srt"`
	Count       int    `json:"count"`
}

func SavePreparedSubtitle(task CreatedTask, srt string, count int) error {
	hash, err := VideoDigest(task.Path)
	if err != nil {
		return err
	}
	return saveTaskJSON(filepath.Join(task.Dir, "subtitle-prepared.json"), PreparedSubtitle{Version: 1, VideoSHA256: hash, SRT: srt, Count: count})
}

func LoadPreparedSubtitle(task CreatedTask) (PreparedSubtitle, bool, error) {
	var result PreparedSubtitle
	body, err := os.ReadFile(filepath.Join(task.Dir, "subtitle-prepared.json"))
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if json.Unmarshal(body, &result) != nil || result.Version != 1 || result.VideoSHA256 == "" || result.SRT == "" || result.Count < 1 {
		return result, true, requestError("已保存转写记录无效，请保留任务")
	}
	hash, err := VideoDigest(task.Path)
	if err != nil || hash != result.VideoSHA256 {
		return result, true, requestError("视频与已保存转写记录不一致，请保留任务")
	}
	return result, true, nil
}
