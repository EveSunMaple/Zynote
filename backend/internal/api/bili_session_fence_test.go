package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

type fencedBiliStore struct {
	state   *contract.State
	reverse contract.ReverseCaller
}

func (store *fencedBiliStore) State() *contract.State          { return store.state }
func (store *fencedBiliStore) SetState(state *contract.State)  { store.state = state }
func (store *fencedBiliStore) Reverse() contract.ReverseCaller { return store.reverse }

type fencedBiliReverse struct {
	mu      sync.Mutex
	events  []string
	entered chan struct{}
	release chan struct{}
}

func (reverse *fencedBiliReverse) Call(method string, _ any, _ int) (json.RawMessage, error) {
	if method == "shell.biliOpen" {
		close(reverse.entered)
		<-reverse.release
	}
	reverse.mu.Lock()
	reverse.events = append(reverse.events, method)
	reverse.mu.Unlock()
	return json.RawMessage(`true`), nil
}

func TestBrowserOpenAcknowledgesBeforeLogoutClearsSnapshot(t *testing.T) {
	inv := NewInvoker()
	state := &contract.State{LibraryRoot: t.TempDir()}
	root := filepath.Join(state.LibraryRoot, "MediaDownloads")
	path := filepath.Join(root, "bili-session", "cookies.dpapi")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[{"name":"SESSDATA","value":"old-fixture","domain":"bilibili.com","path":"/","secure":true}]`), 0600); err != nil {
		t.Fatal(err)
	}
	inv.biliRoot = root
	inv.bili = bilibili.NewResources(root, func(body []byte, _ bool) ([]byte, error) { return body, nil })
	reverse := &fencedBiliReverse{entered: make(chan struct{}), release: make(chan struct{})}
	store := &fencedBiliStore{state: state, reverse: reverse}
	opened := make(chan error, 1)
	go func() { _, err := inv.handleBiliOpen(store, state, json.RawMessage(`{}`)); opened <- err }()
	<-reverse.entered
	if inv.biliSessionMu.TryLock() {
		inv.biliSessionMu.Unlock()
		t.Fatal("cookie snapshot was not fenced through shell ACK")
	}
	loggedOut := make(chan error, 1)
	go func() { _, err := inv.handleBiliLogout(store, state, nil); loggedOut <- err }()
	close(reverse.release)
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	if err := <-loggedOut; err != nil {
		t.Fatal(err)
	}
	if len(reverse.events) != 2 || reverse.events[0] != "shell.biliOpen" || reverse.events[1] != "shell.biliClear" {
		t.Fatal(reverse.events)
	}
	if cookies, err := inv.bili.BrowserCookies(); err != nil || len(cookies) != 0 {
		t.Fatal("logout retained cookies", cookies, err)
	}
}
