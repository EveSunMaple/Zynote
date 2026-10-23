package platform

import (
	"context"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

type Dispatcher struct {
	Bili    *bilibili.CatalogReader
	Youtube NativeCaller
}

func NewDispatcher(bili *bilibili.CatalogReader) *Dispatcher { return &Dispatcher{Bili: bili} }

func (dispatcher *Dispatcher) Read(input string, playlist bool, options any) (any, error) {
	return dispatcher.Bili.Read(input, playlist, options)
}

func (dispatcher *Dispatcher) ReadWithFetcher(ctx context.Context, input string, playlist bool, options any, fetcher media.AuthFetcher) (any, error) {
	selection, err := Input(input)
	if err != nil {
		return nil, err
	}
	settings, err := media.MediaOptions(options)
	if err != nil {
		return nil, err
	}
	if selection.Platform == "bilibili" {
		if settings.CookieProfile != "" {
			return bilibili.NewCatalogReader(bilibili.NewScopedResources(bilibili.ScopedFetcher(fetcher))).Read(input, playlist, settings)
		}
		return dispatcher.Bili.Read(input, playlist, settings)
	}
	return Read(ctx, selection, playlist, settings, fetcher, dispatcher.Youtube)
}
