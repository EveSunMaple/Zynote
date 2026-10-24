package app

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/api"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/helper"
)

type App struct {
	mu       sync.Mutex
	state    *contract.State
	reverse  contract.ReverseCaller
	notifier contract.Notifier
	invoker  *api.Invoker
}

func New() *App {
	return &App{invoker: api.NewInvoker()}
}

func (a *App) Wire(reverse contract.ReverseCaller) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reverse = reverse
}

func (a *App) SetNotifier(notifier contract.Notifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notifier = notifier
	a.invoker.SetNotifier(notifier)
}

func (a *App) SetNative(native *helper.Native) {
	a.invoker.SetNative(native)
}

func (a *App) SetYoutubeNative(native *helper.Native) {
	a.invoker.SetYoutubeNative(native)
}

func (a *App) SetEmbedNative(native *helper.Native) {
	a.invoker.SetEmbedNative(native)
}

func (a *App) SetAsrNative(native *helper.Native) {
	a.invoker.SetAsrNative(native)
}

func (a *App) State() *contract.State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *App) SetState(state *contract.State) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = state
}

func (a *App) Reverse() contract.ReverseCaller {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reverse
}

func (a *App) Notify(method string, params any) error {
	a.mu.Lock()
	notifier := a.notifier
	a.mu.Unlock()
	if notifier == nil {
		return nil
	}
	return notifier.Notify(method, params)
}

func (a *App) Invoke(action string, params json.RawMessage) (any, error) {
	return a.invoker.Invoke(a, action, params)
}

func (a *App) LogHello(state *contract.State) {
	slog.Info("hello accepted", "dataRoot", state.DataRoot)
}

var _ contract.StateStore = (*App)(nil)
var _ contract.Handler = (*App)(nil)
var _ = json.Marshal
