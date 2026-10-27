package im

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/EveSunMaple/Zynote/backend/internal/research"
)

const msgLoginIncomplete = "登录未完成，请检查所选连接状态。"

type Login struct {
	Projection map[string]any
	Revision   string
	Qq         *QqLogin
	ILink      *ILinkSession
	Web        *WeixinWebSession
	QR         string
	CreatedAt  int64
	Cancel     context.CancelFunc
}

type Service struct {
	Store      *Store
	Root       string
	Resources  string
	mu         sync.Mutex
	logins     map[string]*Login
	syncing    map[string]bool
	profile    string
	Model      func(ctx context.Context, system, user string) (string, error)
	Knowledge  *Knowledge
	automation *Automation
}

type Knowledge struct {
	ID      string
	Sources *research.Sources
}

func NewService(root, resources string, crypt Crypt) (*Service, error) {
	store, err := Open(root, crypt)
	if err != nil {
		return nil, err
	}
	service := &Service{Store: store, Root: root, Resources: resources, logins: map[string]*Login{}, syncing: map[string]bool{}}
	automation, err := NewAutomation(service)
	if err != nil {
		store.Close()
		return nil, err
	}
	service.automation = automation
	return service, nil
}

func (service *Service) serviceModelProfile() string {
	if service.profile == "" {
		return ""
	}
	return "native-" + service.profile
}

func (service *Service) Close() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.automation.PauseAll()
	service.automation.Wait(3 * time.Second)
	for _, login := range service.logins {
		login.Cancel()
	}
	return service.Store.Close()
}

func availability(platform string, fields map[string]string) (bool, string) {
	if platform == "weixin_local" {
		return false, "本机微信读取尚未完成 Go 原生适配；仍可查看已保存消息。"
	}
	if platform == "qq_napcat" && fields["mode"] != "manual" {
		return false, "内置 QQ 后台尚未完成 Go 原生适配；手工连接已有 QQ 服务不受此限制。"
	}
	return true, ""
}

func requireAvailable(row Connection) error {
	if available, reason := availability(row.Platform, row.Fields); !available {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

func (service *Service) Execute(ctx context.Context, action string, input map[string]any, profile string) (any, error) {
	service.mu.Lock()
	if profile != "" && profile != service.profile {
		service.automation.PauseAll()
		service.profile = profile
	}
	service.mu.Unlock()
	if input == nil {
		input = map[string]any{}
	}
	switch action {
	case "status":
		listing, err := service.Store.Listing()
		if err != nil {
			return nil, err
		}
		connections := listing["connections"].([]Connection)
		values := []map[string]any{}
		for _, row := range connections {
			available, reason := availability(row.Platform, row.Fields)
			encoded, _ := json.Marshal(row)
			value := map[string]any{}
			_ = json.Unmarshal(encoded, &value)
			value["live"] = map[string]any{"available": available, "reason": reason}
			values = append(values, value)
		}
		managedAvailable, managedReason := availability("qq_napcat", map[string]string{"mode": "managed"})
		localAvailable, localReason := availability("weixin_local", map[string]string{})
		return map[string]any{
			"revision": listing["revision"], "connections": values,
			"platforms": map[string]any{
				"qq_napcat":    map[string]any{"label": "个人 QQ · 内置后台", "available": managedAvailable, "reason": managedReason},
				"weixin_local": map[string]any{"label": "个人微信 · 本机只读", "available": localAvailable, "reason": localReason},
				"weixin_web":   map[string]any{"label": "微信 Web · 实验", "available": true, "reason": ""},
				"weixin_ilink": map[string]any{"label": "微信 Bot · iLink", "available": true, "reason": ""},
			},
			"qq_component": map[string]any{"available": false, "state": "unavailable"},
		}, nil
	case "change":
		platform := text(input["platform"])
		fields := stringMap(input["fields"])
		if action, _ := input["action"].(string); action == "create" {
			if available, reason := availability(platform, fields); !available {
				return nil, fmt.Errorf("%s", reason)
			}
		}
		return service.Store.Change(input)
	case "model":
		return map[string]any{"profile_id": service.serviceModelProfile(), "model": "Zynote当前连接"}, nil
	case "libraries":
		libraries := []any{}
		if service.Knowledge != nil {
			libraries = append(libraries, map[string]any{"library_id": service.Knowledge.ID, "label": "当前知识库", "available": true})
		}
		return map[string]any{"libraries": libraries}, nil
	case "saved-directory":
		return service.Store.SavedDirectory(input)
	case "read":
		return service.Store.ReadMessages(input)
	case "send-status":
		return service.Store.SendStatus(input)
	case "cleanup":
		return service.Cleanup(input)
	case "start":
		return service.Start(ctx, input)
	case "inspect", "cancel", "confirm", "qr":
		return service.loginAction(ctx, action, input)
	case "directory", "sync", "send":
		return service.scopeAction(ctx, action, input)
	case "personal-processes", "personal-probe", "personal-confirm":
		return nil, fmt.Errorf("本机微信读取暂仅支持 Windows x64")
	case "rules":
		return service.automation.Rules(input)
	case "runs":
		return service.automation.Runs(input)
	case "reply-jobs":
		return service.automation.Jobs(input)
	}
	if strings.HasPrefix(action, "personal-") {
		return nil, fmt.Errorf("本机微信读取暂仅支持 Windows x64")
	}
	return nil, fmt.Errorf("此通讯操作尚未完成原生适配：%s", action)
}

func (service *Service) persist(login *Login) map[string]any {
	scope, _ := json.Marshal(map[string]any{"version": "native-v1", "connection_id": text(login.Projection["connection_id"]), "revision": login.Revision})
	projection, _ := json.Marshal(login.Projection)
	_, _ = service.Store.db.Exec("INSERT INTO login_operations VALUES(?,?,?) ON CONFLICT(operation_id) DO UPDATE SET projection=excluded.projection",
		text(login.Projection["operation_id"]), string(scope), string(projection))
	return login.Projection
}

func (service *Service) operation(input map[string]any) (string, string, string, *Login, map[string]any, error) {
	op := text(input["operation_id"])
	connectionID := text(input["connection_id"])
	generation := text(input["generation"])
	if !idPattern.MatchString(op) || !idPattern.MatchString(connectionID) || !idPattern.MatchString(generation) {
		return "", "", "", nil, nil, fmt.Errorf("登录操作参数无效")
	}
	service.mu.Lock()
	login := service.logins[op]
	service.mu.Unlock()
	if login != nil {
		if text(login.Projection["connection_id"]) != connectionID || text(login.Projection["generation"]) != generation {
			return "", "", "", nil, nil, fmt.Errorf("登录操作归属不符")
		}
		return op, connectionID, generation, login, login.Projection, nil
	}
	var projection string
	if err := service.Store.db.QueryRow("SELECT projection FROM login_operations WHERE operation_id=?", op).Scan(&projection); err != nil {
		return "", "", "", nil, nil, fmt.Errorf("登录操作不存在")
	}
	saved := map[string]any{}
	_ = json.Unmarshal([]byte(projection), &saved)
	if text(saved["connection_id"]) != connectionID || text(saved["generation"]) != generation {
		return "", "", "", nil, nil, fmt.Errorf("登录操作归属已改变")
	}
	return op, connectionID, generation, nil, saved, nil
}

func (service *Service) current(login *Login) error {
	if login == nil {
		return fmt.Errorf("本次登录状态已中断")
	}
	login.Cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	_ = ctx
	login.Cancel = cancel
	if time.Now().UnixMilli()-login.CreatedAt > 10*60*1000 {
		cancel()
		return fmt.Errorf("本次登录已过期，请重新开始")
	}
	if _, err := service.Store.Capture(text(login.Projection["connection_id"]), login.Revision); err != nil {
		cancel()
		return err
	}
	return nil
}

func (service *Service) Start(ctx context.Context, input map[string]any) (any, error) {
	op := text(input["operation_id"])
	connectionID := text(input["connection_id"])
	expected := text(input["expected_revision"])
	if !idPattern.MatchString(op) || !idPattern.MatchString(connectionID) || !idPattern.MatchString(expected) {
		return nil, fmt.Errorf("登录参数无效")
	}
	row, err := service.Store.Capture(connectionID, expected)
	if err != nil {
		return nil, err
	}
	if err := requireAvailable(row); err != nil {
		return nil, err
	}
	var projection string
	if err := service.Store.db.QueryRow("SELECT projection FROM login_operations WHERE operation_id=?", op).Scan(&projection); err == nil {
		saved := map[string]any{}
		_ = json.Unmarshal([]byte(projection), &saved)
		if text(saved["connection_id"]) != row.ID {
			return nil, fmt.Errorf("登录编号已用于其他连接")
		}
		state := text(saved["state"])
		if state == "online" {
			state = "online"
		} else {
			state = "unknown"
		}
		saved["state"] = state
		saved["has_qr"] = false
		saved["detail"] = map[string]any{"message": "已有操作不自动重复启动；中断后请明确新建登录操作。"}
		return saved, nil
	}
	if row.Platform != "qq_napcat" && row.Platform != "weixin_ilink" && row.Platform != "weixin_web" {
		return nil, fmt.Errorf("此平台的原生登录适配仍在迁移，未启动旧后台")
	}
	service.mu.Lock()
	for _, login := range service.logins {
		if text(login.Projection["connection_id"]) == row.ID {
			state := text(login.Projection["state"])
			if state != "online" && state != "cancelled" && state != "failed" && state != "expired" {
				login.Cancel()
				login.Projection["state"] = "superseded"
				login.Projection["has_qr"] = false
				service.persist(login)
			}
		}
	}
	service.mu.Unlock()
	loginCtx, cancel := context.WithCancel(ctx)
	login := &Login{
		Projection: map[string]any{
			"operation_id": op, "connection_id": row.ID, "generation": newID(),
			"state": "verifying", "account": "", "has_qr": false, "createdAt": float64(time.Now().UnixMilli()),
		},
		Revision: row.Revision, CreatedAt: time.Now().UnixMilli(), Cancel: cancel,
	}
	service.mu.Lock()
	service.logins[op] = login
	service.mu.Unlock()
	service.persist(login)
	if row.Platform == "weixin_web" {
		adapter, err := NewWeixinWebSession(nil)
		if err != nil {
			cancel()
			return nil, err
		}
		login.Web = adapter
		state, err := adapter.Start(loginCtx)
		if err != nil {
			cancel()
			login.Projection["state"] = "failed"
			login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
			service.persist(login)
			return nil, err
		}
		login.QR = text(state["qr"])
		login.Projection["state"] = text(state["state"])
		login.Projection["has_qr"] = login.QR != ""
		return service.persist(login), nil
	}
	if row.Platform == "weixin_ilink" {
		adapter := &ILinkSession{Contexts: map[string]string{}}
		login.ILink = adapter
		state, err := adapter.Start(loginCtx)
		if err != nil {
			cancel()
			login.Projection["state"] = "failed"
			login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
			service.persist(login)
			return nil, err
		}
		login.QR = text(state["qr"])
		login.Projection["state"] = text(state["state"])
		login.Projection["has_qr"] = login.QR != ""
		return service.persist(login), nil
	}
	privateRow, err := service.Store.PrivateState(row.ID, row.Revision)
	if err != nil {
		cancel()
		return nil, err
	}
	if row.Fields["mode"] != "manual" {
		cancel()
		login.Projection["state"] = "failed"
		login.Projection["detail"] = map[string]any{"message": "内置 QQ 后台暂仅支持 Windows x64。"}
		service.persist(login)
		return nil, fmt.Errorf("内置 QQ 后台暂仅支持 Windows x64")
	}
	onebot, err := parseQqConfig(map[string]any{"url": row.Fields["onebot_url"], "token": privateRow.Secrets["onebot_token"]})
	if err != nil {
		cancel()
		return nil, err
	}
	adapter, err := NewQqLogin(row.Fields["webui_url"], privateRow.Secrets["webui_token"], onebot, false)
	if err != nil {
		cancel()
		return nil, err
	}
	login.Qq = adapter
	state, err := adapter.Start(loginCtx)
	if err != nil {
		if err != context.Canceled {
			login.Projection["state"] = "failed"
			login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
			service.persist(login)
		}
		return nil, err
	}
	login.QR = text(state["qr"])
	login.Projection["state"] = text(state["state"])
	login.Projection["has_qr"] = login.QR != ""
	service.persist(login)
	if text(state["state"]) == "verifying" {
		return service.inspectLogin(loginCtx, login)
	}
	return login.Projection, nil
}

func (service *Service) loginAction(ctx context.Context, action string, input map[string]any) (any, error) {
	_, connectionID, _, login, saved, err := service.operation(input)
	if err != nil {
		return nil, err
	}
	if login == nil {
		if action == "qr" {
			return nil, fmt.Errorf("中断后的二维码不恢复，请重新开始登录")
		}
		state := text(saved["state"])
		if state == "online" {
			state = "online"
		} else {
			state = "unknown"
		}
		saved["state"] = state
		saved["has_qr"] = false
		return saved, nil
	}
	if action != "cancel" {
		row, err := service.Store.Capture(connectionID, "")
		if err != nil {
			return nil, err
		}
		if err := requireAvailable(row); err != nil {
			return nil, err
		}
	}
	if action == "cancel" {
		if text(login.Projection["state"]) != "online" {
			login.Cancel()
			login.QR = ""
			login.Projection["state"] = "cancelled"
			login.Projection["has_qr"] = false
			service.persist(login)
		}
		return login.Projection, nil
	}
	if action == "qr" {
		if login.QR == "" {
			return nil, fmt.Errorf("当前登录尚未取得二维码")
		}
		png, err := qrcode.Encode(login.QR, qrcode.Medium, -5)
		if err != nil {
			return nil, fmt.Errorf("二维码生成失败")
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
	}
	if action == "inspect" {
		if code, ok := input["verify_code"]; ok && code != nil {
			if login.ILink == nil {
				return nil, fmt.Errorf("此连接不使用手机配对码")
			}
			if err := login.ILink.SubmitCode(text(code)); err != nil {
				return nil, err
			}
		}
		if text(login.Projection["state"]) == "online" {
			return service.persist(login), nil
		}
		return service.inspectLogin(ctx, login)
	}
	if action == "confirm" {
		if text(login.Projection["state"]) == "online" {
			return map[string]any{"login": service.persist(login)}, nil
		}
		displayed := text(input["displayed_account"])
		if text(login.Projection["state"]) != "awaiting_bind" || displayed != text(login.Projection["account"]) {
			return nil, fmt.Errorf("请先核对本次显示的账号")
		}
		result, err := service.verifyLogin(ctx, login)
		if err != nil {
			return nil, err
		}
		account := text(result["account"])
		if account != text(login.Projection["account"]) {
			return nil, fmt.Errorf("确认期间登录账号改变，未绑定")
		}
		bound, err := service.Store.Bind(text(login.Projection["connection_id"]), login.Revision, account, result["session"], result["capabilities"], func() error {
			if text(login.Projection["state"]) != "awaiting_bind" || displayed != text(login.Projection["account"]) {
				return fmt.Errorf("确认期间登录状态或账号已改变，未绑定")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		login.QR = ""
		login.Projection["state"] = "online"
		login.Projection["has_qr"] = false
		login.Projection["boundRevision"] = bound.Revision
		return map[string]any{"login": service.persist(login)}, nil
	}
	return nil, fmt.Errorf("登录操作无效")
}

func (service *Service) inspectLogin(ctx context.Context, login *Login) (any, error) {
	if err := service.current(login); err != nil {
		return nil, err
	}
	if login.Qq != nil {
		state, err := login.Qq.Poll(ctx)
		if err != nil {
			login.Projection["state"] = "failed"
			login.Projection["has_qr"] = false
			login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
			service.persist(login)
			return nil, err
		}
		login.QR = text(state["qr"])
		login.Projection["state"] = text(state["state"])
		login.Projection["has_qr"] = login.QR != ""
	} else if login.ILink != nil {
		state, err := login.ILink.Poll(ctx)
		if err != nil {
			login.Projection["state"] = "failed"
			login.Projection["has_qr"] = false
			login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
			service.persist(login)
			return nil, err
		}
		login.QR = text(state["qr"])
		login.Projection["state"] = text(state["state"])
		login.Projection["has_qr"] = login.QR != ""
		if detail, ok := state["detail"]; ok {
			login.Projection["detail"] = detail
		}
	} else if login.Web != nil {
		state, err := login.Web.Poll(ctx)
		if err != nil {
			login.Projection["state"] = "failed"
			login.Projection["has_qr"] = false
			login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
			service.persist(login)
			return nil, err
		}
		login.QR = text(state["qr"])
		login.Projection["state"] = text(state["state"])
		login.Projection["has_qr"] = login.QR != ""
	} else {
		return nil, fmt.Errorf("本次登录状态已中断")
	}
	if text(login.Projection["state"]) == "verifying" {
		result, err := service.verifyLogin(ctx, login)
		if err != nil {
			login.Projection["state"] = "failed"
			login.Projection["detail"] = map[string]any{"message": err.Error()}
			service.persist(login)
			return nil, err
		}
		login.Projection["state"] = "awaiting_bind"
		login.Projection["account"] = text(result["account"])
		login.Projection["has_qr"] = false
		login.QR = ""
	}
	return service.persist(login), nil
}

func (service *Service) verifyLogin(ctx context.Context, login *Login) (map[string]any, error) {
	if err := service.current(login); err != nil {
		return nil, err
	}
	if login.Qq != nil {
		return login.Qq.Verify(ctx)
	}
	if login.ILink != nil {
		return login.ILink.Verify(ctx, text(login.Projection["account"]))
	}
	if login.Web != nil {
		return login.Web.Verify(ctx, text(login.Projection["account"]))
	}
	return nil, fmt.Errorf("本次登录状态已中断")
}

func (service *Service) Sync(ctx context.Context, scope Scope) (any, error) {
	row, err := service.Store.Capture(scope.ConnectionID, scope.Revision)
	if err != nil {
		return nil, err
	}
	private, err := service.Store.PrivateState(scope.ConnectionID, scope.Revision)
	if err != nil {
		return nil, err
	}
	service.mu.Lock()
	if service.syncing[scope.ConnectionID] {
		service.mu.Unlock()
		return nil, fmt.Errorf("此账号正在同步，请待本次完成后再试")
	}
	service.syncing[scope.ConnectionID] = true
	service.mu.Unlock()
	defer func() {
		service.mu.Lock()
		delete(service.syncing, scope.ConnectionID)
		service.mu.Unlock()
	}()
	if err := service.Store.RequireRead(scope); err != nil {
		return nil, err
	}
	if row.Platform == "weixin_ilink" {
		session, err := NewILinkSession(private.Session)
		if err != nil {
			return nil, err
		}
		updates, err := session.Receive(ctx, scope.Peer, scope.Account)
		if err != nil {
			return nil, err
		}
		return service.Store.SaveSessionAndMessages(scope, session.Session(), updates.Messages, private.Snapshot, nil)
	}
	if row.Platform == "weixin_web" {
		session, err := NewWeixinWebSession(private.Session)
		if err != nil {
			return nil, err
		}
		if _, err := session.Verify(ctx, scope.Account); err != nil {
			return nil, err
		}
		updates, err := session.Receive(ctx, scope.Peer, scope.Account)
		if err != nil {
			return nil, err
		}
		return service.Store.SaveSessionAndMessages(scope, updates.Session, updates.Messages, private.Snapshot, nil)
	}
	if row.Platform == "qq_napcat" {
		session, err := NewQqSession(private.Session)
		if err != nil {
			return nil, err
		}
		if _, err := session.Verify(ctx, scope.Account); err != nil {
			return nil, err
		}
		messages, err := session.Receive(ctx, scope.Peer, scope.Account)
		if err != nil {
			return nil, err
		}
		return service.Store.SaveMessages(scope, messages)
	}
	return nil, fmt.Errorf("此平台的原生通讯会话仍在迁移")
}

func (service *Service) scopeAction(ctx context.Context, action string, input map[string]any) (any, error) {
	scope, row, err := service.Store.CheckScope(input)
	if err != nil {
		return nil, err
	}
	if err := requireAvailable(row); err != nil {
		return nil, err
	}
	if row.Platform == "weixin_local" {
		if action == "send" {
			return nil, fmt.Errorf("本机个人微信仅支持只读和草稿")
		}
		return nil, fmt.Errorf("本机微信读取暂仅支持 Windows x64")
	}
	if row.Platform != "qq_napcat" && row.Platform != "weixin_ilink" && row.Platform != "weixin_web" {
		return nil, fmt.Errorf("此平台的原生通讯会话仍在迁移")
	}
	private, err := service.Store.PrivateState(scope.ConnectionID, scope.Revision)
	if err != nil {
		return nil, err
	}
	if action == "send" {
		fresh, receipt, err := service.Store.ClaimSend(input)
		if err != nil {
			return nil, err
		}
		if !fresh {
			if text(receipt["state"]) == "sending" {
				receipt["state"] = "unknown"
			}
			return receipt, nil
		}
		op := text(input["operation_id"])
		message := text(input["text"])
		var mid string
		if row.Platform == "weixin_ilink" {
			session, err := NewILinkSession(private.Session)
			if err == nil {
				err = func() error {
					if _, verifyErr := session.Verify(ctx, scope.Account); verifyErr != nil {
						return verifyErr
					}
					mid, err = session.Send(ctx, scope.Peer, message, op)
					return err
				}()
			}
			if err != nil {
				_, _ = service.Store.FinishSend(op, "unknown", "")
				return map[string]any{"operation_id": op, "state": "unknown"}, nil
			}
		} else if row.Platform == "weixin_web" {
			session, err := NewWeixinWebSession(private.Session)
			if err != nil {
				_, _ = service.Store.FinishSend(op, "unknown", "")
				return map[string]any{"operation_id": op, "state": "unknown"}, nil
			}
			if _, err := session.Verify(ctx, scope.Account); err != nil {
				_, _ = service.Store.FinishSend(op, "unknown", "")
				return map[string]any{"operation_id": op, "state": "unknown"}, nil
			}
			mid, err = session.Send(ctx, scope.Peer, message, op)
			if err != nil {
				_, _ = service.Store.FinishSend(op, "unknown", "")
				return map[string]any{"operation_id": op, "state": "unknown"}, nil
			}
		} else if row.Platform == "qq_napcat" {
			session, err := NewQqSession(private.Session)
			if err != nil {
				_, _ = service.Store.FinishSend(op, "unknown", "")
				return map[string]any{"operation_id": op, "state": "unknown"}, nil
			}
			if _, err := session.Verify(ctx, scope.Account); err != nil {
				_, _ = service.Store.FinishSend(op, "unknown", "")
				return map[string]any{"operation_id": op, "state": "unknown"}, nil
			}
			mid, err = session.Send(ctx, scope.Peer, message)
			if err != nil {
				_, _ = service.Store.FinishSend(op, "unknown", "")
				return map[string]any{"operation_id": op, "state": "unknown"}, nil
			}
		} else {
			_, _ = service.Store.FinishSend(op, "unknown", "")
			return map[string]any{"operation_id": op, "state": "unknown"}, nil
		}
		receipt, err = service.Store.FinishSend(op, "sent", mid)
		if err != nil {
			return nil, err
		}
		return receipt, nil
	}
	if action == "sync" {
		return service.Sync(ctx, scope)
	}
	if action == "directory" {
		if err := service.Store.RequireRead(scope); err != nil {
			return nil, err
		}
		if row.Platform == "weixin_ilink" {
			session, err := NewILinkSession(private.Session)
			if err != nil {
				return nil, err
			}
			return session.Directory(ctx)
		}
		if row.Platform == "weixin_web" {
			session, err := NewWeixinWebSession(private.Session)
			if err != nil {
				return nil, err
			}
			if _, err := session.Verify(ctx, scope.Account); err != nil {
				return nil, err
			}
			return session.Directory(ctx)
		}
		if row.Platform == "qq_napcat" {
			session, err := NewQqSession(private.Session)
			if err != nil {
				return nil, err
			}
			if _, err := session.Verify(ctx, scope.Account); err != nil {
				return nil, err
			}
			return session.Directory(ctx)
		}
		return nil, fmt.Errorf("此平台的原生通讯会话仍在迁移")
	}
	return nil, fmt.Errorf("此通讯操作尚未完成原生适配：%s", action)
}
