package api

import (
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	mediaBilibili "github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

const msgInvalidEpisode = "所选分集无效"

func selectedEpisodes(series mediaBilibili.VideoSeries, pages []int) ([]mediaBilibili.VideoEpisode, error) {
	if len(pages) < 1 || len(pages) > 1000 {
		return nil, contract.InputInvalid(msgInvalidEpisode)
	}
	seen := map[int]bool{}
	selected := []mediaBilibili.VideoEpisode{}
	for _, page := range pages {
		if seen[page] {
			return nil, contract.InputInvalid(msgInvalidEpisode)
		}
		seen[page] = true
		found := false
		for _, episode := range series.Episodes {
			if episode.Page == page {
				selected = append(selected, episode)
				found = true
				break
			}
		}
		if !found {
			return nil, contract.InputInvalid(msgInvalidEpisode)
		}
	}
	return selected, nil
}
