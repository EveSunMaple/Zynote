package im

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/helper"
)

type weixinProbe struct {
	Connection string
	Revision   string
	Account    string
	Expires    time.Time
}

// WeixinLocal keeps the short-lived probe/reader ownership state for the Rust
// --mode wechat helper. Session data only contains reader_id and cursors.
type WeixinLocal struct {
	mu      sync.Mutex
	native  *helper.Native
	probes  map[string]weixinProbe
	readers map[string]string
	closed  bool
}

func NewWeixinLocal(native *helper.Native) *WeixinLocal {
	return &WeixinLocal{native: native, probes: map[string]weixinProbe{}, readers: map[string]string{}}
}

func (local *WeixinLocal) SetNative(native *helper.Native) {
	local.mu.Lock()
	defer local.mu.Unlock()
	local.native = native
}

func (local *WeixinLocal) Available() bool {
	local.mu.Lock()
	native := local.native
	closed := local.closed
	local.mu.Unlock()
	return weixinSupported() && !closed && native != nil && native.Ready()
}

func (local *WeixinLocal) call(ctx context.Context, method string, params any, timeoutMS int) (map[string]any, error) {
	local.mu.Lock()
	native := local.native
	closed := local.closed
	local.mu.Unlock()
	if !weixinSupported() {
		return nil, fmt.Errorf("本机微信读取暂仅支持 Windows x64")
	}
	if closed || native == nil || !native.Ready() {
		return nil, fmt.Errorf("本机微信读取助手未就绪")
	}
	raw, err := native.CallContext(ctx, method, params, timeoutMS)
	if err != nil {
		return nil, err
	}
	value := map[string]any{}
	if json.Unmarshal(raw, &value) != nil {
		return nil, fmt.Errorf("本机微信读取返回格式无效")
	}
	return value, nil
}

func (local *WeixinLocal) Processes(ctx context.Context) (map[string]any, error) {
	return local.call(ctx, "wechat.processes", map[string]any{}, 20000)
}

func (local *WeixinLocal) Probe(ctx context.Context, connection, revision string, pid int, created float64) (map[string]any, error) {
	result, err := local.call(ctx, "wechat.probe", map[string]any{"pid": pid, "created": created}, 150000)
	if err != nil {
		return nil, err
	}
	probeID := text(result["probe_id"])
	account := text(result["account"])
	if !idPattern.MatchString(probeID) || account == "" || len([]rune(account)) > 200 {
		return nil, fmt.Errorf("本机微信核验结果无效")
	}
	local.mu.Lock()
	defer local.mu.Unlock()
	now := time.Now()
	for key, probe := range local.probes {
		if probe.Expires.Before(now) {
			delete(local.probes, key)
		}
	}
	if len(local.probes) >= 4 {
		return nil, fmt.Errorf("待确认微信读取绑定已达到上限")
	}
	local.probes[probeID] = weixinProbe{Connection: connection, Revision: revision, Account: account, Expires: now.Add(120 * time.Second)}
	return result, nil
}

func (local *WeixinLocal) ProbeFor(connection, revision, probeID, account string) (weixinProbe, error) {
	local.mu.Lock()
	defer local.mu.Unlock()
	probe, ok := local.probes[probeID]
	if !ok || probe.Expires.Before(time.Now()) || probe.Connection != connection || probe.Revision != revision || probe.Account != account {
		return weixinProbe{}, fmt.Errorf("本机微信读取绑定已失效")
	}
	return probe, nil
}

func (local *WeixinLocal) Confirm(ctx context.Context, probeID, account string, verify func(session any) error) (map[string]any, error) {
	result, err := local.call(ctx, "wechat.confirm", map[string]any{"probe_id": probeID, "account": account}, 20000)
	if err != nil {
		return nil, err
	}
	session := result["session"]
	readerID := sessionReaderID(session)
	if readerID == "" {
		return nil, fmt.Errorf("本机微信会话无效")
	}
	if verify != nil {
		if err := verify(session); err != nil {
			_, _ = local.call(context.Background(), "wechat.invalidate", map[string]any{"reader_id": readerID}, 10000)
			return nil, err
		}
	}
	return result, nil
}

func (local *WeixinLocal) ReleaseProbe(probeID string) {
	local.mu.Lock()
	defer local.mu.Unlock()
	delete(local.probes, probeID)
}

func (local *WeixinLocal) Bind(connection, readerID string) (old string) {
	local.mu.Lock()
	defer local.mu.Unlock()
	old = local.readers[connection]
	local.readers[connection] = readerID
	return old
}

func (local *WeixinLocal) InvalidateConnection(ctx context.Context, connection string) {
	local.mu.Lock()
	readerID := local.readers[connection]
	delete(local.readers, connection)
	for key, probe := range local.probes {
		if probe.Connection == connection {
			delete(local.probes, key)
		}
	}
	local.mu.Unlock()
	if readerID != "" {
		_, _ = local.call(ctx, "wechat.invalidate", map[string]any{"reader_id": readerID}, 10000)
	}
}

func (local *WeixinLocal) Verify(ctx context.Context, session any, account string) (map[string]any, error) {
	return local.call(ctx, "wechat.verify", map[string]any{"session": session, "account": account}, 15000)
}

func (local *WeixinLocal) Directory(ctx context.Context, session any, account string) (map[string]any, error) {
	return local.call(ctx, "wechat.directory", map[string]any{"session": session, "account": account}, 20000)
}

func (local *WeixinLocal) Receive(ctx context.Context, session any, account, peer string) (map[string]any, error) {
	return local.call(ctx, "wechat.receive", map[string]any{"session": session, "account": account, "peer": peer}, 25000)
}

func (local *WeixinLocal) Invalidate(ctx context.Context, readerID string) {
	if readerID != "" {
		_, _ = local.call(ctx, "wechat.invalidate", map[string]any{"reader_id": readerID}, 10000)
	}
}

func (local *WeixinLocal) Close() {
	local.mu.Lock()
	readerIDs := make([]string, 0, len(local.readers))
	for _, readerID := range local.readers {
		readerIDs = append(readerIDs, readerID)
	}
	local.readers = map[string]string{}
	local.probes = map[string]weixinProbe{}
	local.mu.Unlock()
	for _, readerID := range readerIDs {
		local.Invalidate(context.Background(), readerID)
	}
	local.mu.Lock()
	local.closed = true
	local.mu.Unlock()
}

func sessionReaderID(session any) string {
	value, _ := session.(map[string]any)
	return text(value["reader_id"])
}

func resultMessages(value map[string]any) ([]Message, error) {
	raw, ok := value["messages"]
	if !ok {
		return nil, fmt.Errorf("本机微信消息返回格式无效")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("本机微信消息返回格式无效")
	}
	messages := []Message{}
	if json.Unmarshal(encoded, &messages) != nil {
		return nil, fmt.Errorf("本机微信消息返回格式无效")
	}
	for index, message := range messages {
		if strings.TrimSpace(message.MID) == "" || len([]rune(message.MID)) > 200 || len([]rune(message.Text)) > 50000 {
			return nil, fmt.Errorf("本机微信消息返回格式无效")
		}
		messages[index] = message
	}
	return messages, nil
}
