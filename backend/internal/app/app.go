package app

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/api"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type App struct {
	mu      sync.Mutex
	state   *contract.State
	reverse contract.ReverseCaller
	invoker *api.Invoker
}

func New() *App {
	return &App{invoker: api.NewInvoker()}
}

func (a *App) Wire(reverse contract.ReverseCaller) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reverse = reverse
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

func (a *App) Invoke(action string, params json.RawMessage) (any, error) {
	return a.invoker.Invoke(a, action, params)
}

func (a *App) LogHello(state *contract.State) {
	slog.Info("hello accepted", "dataRoot", state.DataRoot)
}

var _ contract.StateStore = (*App)(nil)
var _ contract.Handler = (*App)(nil)
var _ = json.Marshal
