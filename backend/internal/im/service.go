package im

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/EveSunMaple/Zynote/backend/internal/helper"
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
	weixin     *WeixinLocal
	napcat     *NapcatRuntime
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
	service := &Service{
		Store: store, Root: root, Resources: resources, logins: map[string]*Login{}, syncing: map[string]bool{},
		weixin: NewWeixinLocal(nil),
		napcat: NewNapcatRuntime(resources, filepath.Join(root, "runtime-components", "napcat"), crypt),
	}
	automation, err := NewAutomation(service)
	if err != nil {
		store.Close()
		return nil, err
	}
	service.automation = automation
	return service, nil
}

// SetWeixinNative installs the shared Rust helper used by the Windows local reader.
func (service *Service) SetWeixinNative(native *helper.Native) {
	service.weixin.SetNative(native)
}

func (service *Service) serviceModelProfile() string {
	if service.profile == "" {
		return ""
	}
	return "native-" + service.profile
}

func (service *Service) Close() error {
	service.mu.Lock()
	service.automation.PauseAll()
	automation := service.automation
	logins := make([]*Login, 0, len(service.logins))
	for _, login := range service.logins {
		logins = append(logins, login)
	}
	service.mu.Unlock()
	automation.Wait(3 * time.Second)
	for _, login := range logins {
		login.Cancel()
	}
	service.weixin.Close()
	service.napcat.Close()
	return service.Store.Close()
}

func (service *Service) availability(platform string, fields map[string]string) (bool, string) {
	if platform == "weixin_local" {
		if !weixinSupported() {
			return false, "本机微信读取暂仅支持 Windows x64；仍可查看已保存消息。"
		}
		if !service.weixin.Available() {
			return false, "本机微信读取助手未就绪；仍可查看已保存消息。"
		}
		return true, ""
	}
	if platform == "qq_napcat" && fields["mode"] != "manual" {
		return service.napcat.Availability()
	}
	return true, ""
}

func (service *Service) requireAvailable(row Connection) error {
	if available, reason := service.availability(row.Platform, row.Fields); !available {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

func (service *Service) connectionStatus(row Connection) map[string]any {
	available, reason := service.availability(row.Platform, row.Fields)
	encoded, _ := json.Marshal(row)
	value := map[string]any{}
	_ = json.Unmarshal(encoded, &value)
	value["live"] = map[string]any{"available": available, "reason": reason}
	return value
}

func (service *Service) statusValue() (any, error) {
	listing, err := service.Store.Listing()
	if err != nil {
		return nil, err
	}
	connections := listing["connections"].([]Connection)
	values := []map[string]any{}
	for _, row := range connections {
		values = append(values, service.connectionStatus(row))
	}
	managedAvailable, managedReason := service.availability("qq_napcat", map[string]string{"mode": "managed"})
	localAvailable, localReason := service.availability("weixin_local", map[string]string{})
	return map[string]any{
		"revision": listing["revision"], "connections": values,
		"platforms": map[string]any{
			"qq_napcat":    map[string]any{"label": "个人 QQ · 内置后台", "available": managedAvailable, "reason": managedReason},
			"weixin_local": map[string]any{"label": "个人微信 · 本机只读", "available": localAvailable, "reason": localReason},
			"weixin_web":   map[string]any{"label": "微信 Web · 实验", "available": true, "reason": ""},
			"weixin_ilink": map[string]any{"label": "微信 Bot · iLink", "available": true, "reason": ""},
		},
		"qq_component": service.napcat.Status(),
	}, nil
}

func (service *Service) changeValue(input map[string]any) (any, error) {
	platform := text(input["platform"])
	fields := stringMap(input["fields"])
	action := text(input["action"])
	if action == "create" {
		if available, reason := service.availability(platform, fields); !available {
			return nil, fmt.Errorf("%s", reason)
		}
	}
	result, err := service.Store.Change(input)
	if err != nil {
		return nil, err
	}
	if action == "delete" {
		service.weixin.InvalidateConnection(context.Background(), text(input["id"]))
	}
	return result, nil
}

func (service *Service) librariesValue() (any, error) {
	libraries := []any{}
	if service.Knowledge != nil {
		libraries = append(libraries, map[string]any{"library_id": service.Knowledge.ID, "label": "当前知识库", "available": true})
	}
	return map[string]any{"libraries": libraries}, nil
}

func (service *Service) personalProcesses(ctx context.Context) (any, error) {
	if !service.weixin.Available() {
		return nil, fmt.Errorf("本机微信读取助手未就绪")
	}
	return service.weixin.Processes(ctx)
}

func (service *Service) personalProbe(ctx context.Context, input map[string]any) (any, error) {
	if !service.weixin.Available() {
		return nil, fmt.Errorf("本机微信读取助手未就绪")
	}
	connectionID := text(input["connection_id"])
	revision := text(input["revision"])
	pid := int(number(input["pid"]))
	created := number(input["created"])
	if !idPattern.MatchString(connectionID) || !idPattern.MatchString(revision) || pid <= 0 || created <= 0 {
		return nil, fmt.Errorf("本机微信核验参数无效")
	}
	row, err := service.Store.Capture(connectionID, revision)
	if err != nil {
		return nil, err
	}
	if row.Platform != "weixin_local" {
		return nil, fmt.Errorf("请先选择本机微信连接")
	}
	result, err := service.weixin.Probe(ctx, connectionID, revision, pid, created)
	if err != nil {
		return nil, err
	}
	if _, err := service.Store.Capture(connectionID, revision); err != nil {
		return nil, err
	}
	return result, nil
}

func (service *Service) personalConfirm(ctx context.Context, input map[string]any) (any, error) {
	if !service.weixin.Available() {
		return nil, fmt.Errorf("本机微信读取助手未就绪")
	}
	connectionID := text(input["connection_id"])
	revision := text(input["revision"])
	probeID := text(input["probe_id"])
	account := text(input["account"])
	confirmed, _ := input["user_confirmed"].(bool)
	if !idPattern.MatchString(connectionID) || !idPattern.MatchString(revision) ||
		!idPattern.MatchString(probeID) || account == "" || len([]rune(account)) > 200 || !confirmed {
		return nil, fmt.Errorf("本机微信绑定参数无效")
	}
	row, err := service.Store.Capture(connectionID, revision)
	if err != nil {
		return nil, err
	}
	if row.Platform != "weixin_local" {
		return nil, fmt.Errorf("当前连接不是本机微信")
	}
	probe, err := service.weixin.ProbeFor(connectionID, revision, probeID, account)
	if err != nil {
		return nil, err
	}
	result, err := service.weixin.Confirm(ctx, probeID, account, nil)
	if err != nil {
		return nil, err
	}
	session := result["session"]
	readerID := sessionReaderID(session)
	if readerID == "" {
		return nil, fmt.Errorf("本机微信会话无效")
	}
	_, err = service.Store.Bind(connectionID, revision, account, session, result["capabilities"], func() error {
		if _, err := service.weixin.ProbeFor(connectionID, revision, probeID, probe.Account); err != nil {
			return err
		}
		_, err := service.weixin.Verify(ctx, session, account)
		return err
	})
	if err != nil {
		service.weixin.Invalidate(ctx, readerID)
		service.weixin.ReleaseProbe(probeID)
		return nil, err
	}
	old := service.weixin.Bind(connectionID, readerID)
	service.weixin.ReleaseProbe(probeID)
	if old != "" && old != readerID {
		service.weixin.Invalidate(ctx, old)
	}
	return map[string]any{"account": account, "session": session, "capabilities": result["capabilities"]}, nil
}

func loginDispatch(ctx context.Context, service *Service, action string, input map[string]any) (any, error) {
	return service.loginAction(ctx, action, input)
}

var imActions = map[string]func(context.Context, *Service, map[string]any) (any, error){
	"status": func(_ context.Context, service *Service, _ map[string]any) (any, error) {
		return service.statusValue()
	},
	"change": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.changeValue(input)
	},
	"model": func(_ context.Context, service *Service, _ map[string]any) (any, error) {
		return map[string]any{"profile_id": service.serviceModelProfile(), "model": "Zynote当前连接"}, nil
	},
	"libraries": func(_ context.Context, service *Service, _ map[string]any) (any, error) {
		return service.librariesValue()
	},
	"saved-directory": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.Store.SavedDirectory(input)
	},
	"read": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.Store.ReadMessages(input)
	},
	"send-status": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.Store.SendStatus(input)
	},
	"cleanup": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.Cleanup(input)
	},
	"start": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return service.Start(ctx, input)
	},
	"inspect": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return loginDispatch(ctx, service, "inspect", input)
	},
	"cancel": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return loginDispatch(ctx, service, "cancel", input)
	},
	"confirm": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return loginDispatch(ctx, service, "confirm", input)
	},
	"qr": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return loginDispatch(ctx, service, "qr", input)
	},
	"directory": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return service.scopeAction(ctx, "directory", input)
	},
	"sync": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return service.scopeAction(ctx, "sync", input)
	},
	"send": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return service.scopeAction(ctx, "send", input)
	},
	"personal-processes": func(ctx context.Context, service *Service, _ map[string]any) (any, error) {
		return service.personalProcesses(ctx)
	},
	"personal-probe": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return service.personalProbe(ctx, input)
	},
	"personal-confirm": func(ctx context.Context, service *Service, input map[string]any) (any, error) {
		return service.personalConfirm(ctx, input)
	},
	"rules": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.automation.Rules(input)
	},
	"runs": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.automation.Runs(input)
	},
	"reply-jobs": func(_ context.Context, service *Service, input map[string]any) (any, error) {
		return service.automation.Jobs(input)
	},
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
	if handler, ok := imActions[action]; ok {
		return handler(ctx, service, input)
	}
	if strings.HasPrefix(action, "personal-") {
		return nil, fmt.Errorf("本机微信操作无效：%s", action)
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

func startParams(input map[string]any) (string, string, string, error) {
	op := text(input["operation_id"])
	connectionID := text(input["connection_id"])
	expected := text(input["expected_revision"])
	if !idPattern.MatchString(op) || !idPattern.MatchString(connectionID) || !idPattern.MatchString(expected) {
		return "", "", "", fmt.Errorf("登录参数无效")
	}
	return op, connectionID, expected, nil
}

func normalizedLoginState(value string) string {
	if value == "online" {
		return "online"
	}
	return "unknown"
}

func (service *Service) existingStart(op, rowID string) (map[string]any, bool, error) {
	var projection string
	if err := service.Store.db.QueryRow("SELECT projection FROM login_operations WHERE operation_id=?", op).Scan(&projection); err != nil {
		return nil, false, nil
	}
	saved := map[string]any{}
	_ = json.Unmarshal([]byte(projection), &saved)
	if text(saved["connection_id"]) != rowID {
		return nil, false, fmt.Errorf("登录编号已用于其他连接")
	}
	saved["state"] = normalizedLoginState(text(saved["state"]))
	saved["has_qr"] = false
	saved["detail"] = map[string]any{"message": "已有操作不自动重复启动；中断后请明确新建登录操作。"}
	return saved, true, nil
}

func (service *Service) supersedeLogins(rowID string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, login := range service.logins {
		if text(login.Projection["connection_id"]) != rowID {
			continue
		}
		state := text(login.Projection["state"])
		if state != "online" && state != "cancelled" && state != "failed" && state != "expired" {
			login.Cancel()
			login.Projection["state"] = "superseded"
			login.Projection["has_qr"] = false
			service.persist(login)
		}
	}
}

func (service *Service) newLogin(op string, row Connection, ctx context.Context) (*Login, context.Context, context.CancelFunc) {
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
	return login, loginCtx, cancel
}

func applyLoginState(login *Login, state map[string]any) {
	login.QR = text(state["qr"])
	login.Projection["state"] = text(state["state"])
	login.Projection["has_qr"] = login.QR != ""
}

func (service *Service) failLogin(login *Login) {
	login.Projection["state"] = "failed"
	login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
	service.persist(login)
}

func (service *Service) startWeixinWeb(loginCtx context.Context, login *Login, cancel context.CancelFunc) (any, error) {
	adapter, err := NewWeixinWebSession(nil)
	if err != nil {
		cancel()
		return nil, err
	}
	login.Web = adapter
	state, err := adapter.Start(loginCtx)
	if err != nil {
		cancel()
		service.failLogin(login)
		return nil, err
	}
	applyLoginState(login, state)
	return service.persist(login), nil
}

func (service *Service) startILink(loginCtx context.Context, login *Login, cancel context.CancelFunc) (any, error) {
	adapter := &ILinkSession{Contexts: map[string]string{}}
	login.ILink = adapter
	state, err := adapter.Start(loginCtx)
	if err != nil {
		cancel()
		service.failLogin(login)
		return nil, err
	}
	applyLoginState(login, state)
	return service.persist(login), nil
}

func (service *Service) startQq(ctx context.Context, loginCtx context.Context, login *Login, cancel context.CancelFunc, row Connection) (any, error) {
	managed := row.Fields["mode"] != "manual"
	webuiURL, webuiToken := row.Fields["webui_url"], ""
	onebotValue := map[string]any{"url": row.Fields["onebot_url"], "token": ""}
	if managed {
		component, err := service.napcat.ensure(ctx)
		if err != nil {
			cancel()
			login.Projection["state"] = "failed"
			login.Projection["detail"] = map[string]any{"message": err.Error()}
			service.persist(login)
			return nil, err
		}
		webuiURL = "http://127.0.0.1:" + strconv.Itoa(component.WebUIPort)
		webuiToken = component.WebUIToken
		onebotValue = map[string]any{
			"url": "http://127.0.0.1:" + strconv.Itoa(component.OneBotPort), "token": component.OneBotToken,
		}
	} else {
		privateRow, err := service.Store.PrivateState(row.ID, row.Revision)
		if err != nil {
			cancel()
			return nil, err
		}
		webuiToken = privateRow.Secrets["webui_token"]
		onebotValue["token"] = privateRow.Secrets["onebot_token"]
	}
	if row.Account != "" {
		onebotValue["account"] = row.Account
	}
	onebot, err := parseQqConfig(onebotValue)
	if err != nil {
		cancel()
		return nil, err
	}
	adapter, err := NewQqLogin(webuiURL, webuiToken, onebot, managed)
	if err != nil {
		cancel()
		return nil, err
	}
	login.Qq = adapter
	state, err := adapter.Start(loginCtx)
	if err != nil {
		if err != context.Canceled {
			service.failLogin(login)
		}
		return nil, err
	}
	applyLoginState(login, state)
	service.persist(login)
	if text(state["state"]) == "verifying" {
		return service.inspectLogin(ctx, login)
	}
	return login.Projection, nil
}

func (service *Service) Start(ctx context.Context, input map[string]any) (any, error) {
	op, connectionID, expected, err := startParams(input)
	if err != nil {
		return nil, err
	}
	row, err := service.Store.Capture(connectionID, expected)
	if err != nil {
		return nil, err
	}
	if err := service.requireAvailable(row); err != nil {
		return nil, err
	}
	if saved, ok, err := service.existingStart(op, row.ID); err != nil || ok {
		return saved, err
	}
	if row.Platform != "qq_napcat" && row.Platform != "weixin_ilink" && row.Platform != "weixin_web" {
		return nil, fmt.Errorf("此平台的原生登录适配仍在迁移，未启动旧后台")
	}
	service.supersedeLogins(row.ID)
	login, loginCtx, cancel := service.newLogin(op, row, ctx)
	switch row.Platform {
	case "weixin_web":
		return service.startWeixinWeb(loginCtx, login, cancel)
	case "weixin_ilink":
		return service.startILink(loginCtx, login, cancel)
	default:
		return service.startQq(ctx, loginCtx, login, cancel, row)
	}
}

func replaySavedLogin(action string, saved map[string]any) (any, error) {
	if action == "qr" {
		return nil, fmt.Errorf("中断后的二维码不恢复，请重新开始登录")
	}
	saved["state"] = normalizedLoginState(text(saved["state"]))
	saved["has_qr"] = false
	return saved, nil
}

func (service *Service) cancelLogin(login *Login) (any, error) {
	if text(login.Projection["state"]) != "online" {
		login.Cancel()
		login.QR = ""
		login.Projection["state"] = "cancelled"
		login.Projection["has_qr"] = false
		service.persist(login)
	}
	return login.Projection, nil
}

func qrLogin(login *Login) (any, error) {
	if login.QR == "" {
		return nil, fmt.Errorf("当前登录尚未取得二维码")
	}
	png, err := qrcode.Encode(login.QR, qrcode.Medium, -5)
	if err != nil {
		return nil, fmt.Errorf("二维码生成失败")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func (service *Service) inspectAction(ctx context.Context, input map[string]any, login *Login) (any, error) {
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

func (service *Service) confirmAction(ctx context.Context, input map[string]any, login *Login) (any, error) {
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

func (service *Service) loginAction(ctx context.Context, action string, input map[string]any) (any, error) {
	_, connectionID, _, login, saved, err := service.operation(input)
	if err != nil {
		return nil, err
	}
	if login == nil {
		return replaySavedLogin(action, saved)
	}
	if action != "cancel" {
		row, err := service.Store.Capture(connectionID, "")
		if err != nil {
			return nil, err
		}
		if err := service.requireAvailable(row); err != nil {
			return nil, err
		}
	}
	switch action {
	case "cancel":
		return service.cancelLogin(login)
	case "qr":
		return qrLogin(login)
	case "inspect":
		return service.inspectAction(ctx, input, login)
	case "confirm":
		return service.confirmAction(ctx, input, login)
	}
	return nil, fmt.Errorf("登录操作无效")
}

func pollLogin(ctx context.Context, login *Login) (map[string]any, error) {
	switch {
	case login.Qq != nil:
		return login.Qq.Poll(ctx)
	case login.ILink != nil:
		return login.ILink.Poll(ctx)
	case login.Web != nil:
		return login.Web.Poll(ctx)
	default:
		return nil, fmt.Errorf("本次登录状态已中断")
	}
}

func (service *Service) failPoll(login *Login, err error) (any, error) {
	login.Projection["state"] = "failed"
	login.Projection["has_qr"] = false
	login.Projection["detail"] = map[string]any{"message": msgLoginIncomplete}
	service.persist(login)
	return nil, err
}

func (service *Service) failVerify(login *Login, err error) (any, error) {
	login.Projection["state"] = "failed"
	login.Projection["detail"] = map[string]any{"message": err.Error()}
	service.persist(login)
	return nil, err
}

func (service *Service) inspectLogin(ctx context.Context, login *Login) (any, error) {
	if err := service.current(login); err != nil {
		return nil, err
	}
	state, err := pollLogin(ctx, login)
	if err != nil {
		return service.failPoll(login, err)
	}
	applyLoginState(login, state)
	if login.ILink != nil {
		if detail, ok := state["detail"]; ok {
			login.Projection["detail"] = detail
		}
	}
	if text(login.Projection["state"]) == "verifying" {
		result, err := service.verifyLogin(ctx, login)
		if err != nil {
			return service.failVerify(login, err)
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

func (service *Service) syncILink(ctx context.Context, scope Scope, private PrivateConnection) (any, error) {
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

func (service *Service) syncWeixinWeb(ctx context.Context, scope Scope, private PrivateConnection) (any, error) {
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

func (service *Service) syncWeixinLocal(ctx context.Context, scope Scope, private PrivateConnection) (any, error) {
	result, err := service.weixin.Receive(ctx, private.Session, scope.Account, scope.Peer)
	if err != nil {
		return nil, err
	}
	messages, err := resultMessages(result)
	if err != nil {
		return nil, err
	}
	return service.Store.SaveSessionAndMessages(scope, result["session"], messages, private.Snapshot, nil)
}

func (service *Service) syncQq(ctx context.Context, scope Scope, private PrivateConnection) (any, error) {
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
	switch row.Platform {
	case "weixin_local":
		return service.syncWeixinLocal(ctx, scope, private)
	case "weixin_ilink":
		return service.syncILink(ctx, scope, private)
	case "weixin_web":
		return service.syncWeixinWeb(ctx, scope, private)
	case "qq_napcat":
		return service.syncQq(ctx, scope, private)
	}
	return nil, fmt.Errorf("此平台的原生通讯会话仍在迁移")
}

func (service *Service) unknownSend(op string) map[string]any {
	_, _ = service.Store.FinishSend(op, "unknown", "")
	return map[string]any{"operation_id": op, "state": "unknown"}
}

func sendILinkMessage(ctx context.Context, scope Scope, private PrivateConnection, message, op string) (string, bool) {
	session, err := NewILinkSession(private.Session)
	if err != nil {
		return "", false
	}
	if _, err := session.Verify(ctx, scope.Account); err != nil {
		return "", false
	}
	mid, err := session.Send(ctx, scope.Peer, message, op)
	if err != nil {
		return "", false
	}
	return mid, true
}

func sendWeixinWebMessage(ctx context.Context, scope Scope, private PrivateConnection, message, op string) (string, bool) {
	session, err := NewWeixinWebSession(private.Session)
	if err != nil {
		return "", false
	}
	if _, err := session.Verify(ctx, scope.Account); err != nil {
		return "", false
	}
	mid, err := session.Send(ctx, scope.Peer, message, op)
	if err != nil {
		return "", false
	}
	return mid, true
}

func sendQqMessage(ctx context.Context, scope Scope, private PrivateConnection, message string) (string, bool) {
	session, err := NewQqSession(private.Session)
	if err != nil {
		return "", false
	}
	if _, err := session.Verify(ctx, scope.Account); err != nil {
		return "", false
	}
	mid, err := session.Send(ctx, scope.Peer, message)
	if err != nil {
		return "", false
	}
	return mid, true
}

func (service *Service) deliverMessage(ctx context.Context, scope Scope, private PrivateConnection, platform, message, op string) (string, bool) {
	switch platform {
	case "weixin_ilink":
		return sendILinkMessage(ctx, scope, private, message, op)
	case "weixin_web":
		return sendWeixinWebMessage(ctx, scope, private, message, op)
	case "qq_napcat":
		return sendQqMessage(ctx, scope, private, message)
	}
	return "", false
}

func (service *Service) sendScope(ctx context.Context, scope Scope, private PrivateConnection, platform string, input map[string]any) (any, error) {
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
	mid, ok := service.deliverMessage(ctx, scope, private, platform, text(input["text"]), op)
	if !ok {
		return service.unknownSend(op), nil
	}
	receipt, err = service.Store.FinishSend(op, "sent", mid)
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

func directoryILink(ctx context.Context, private PrivateConnection) (any, error) {
	session, err := NewILinkSession(private.Session)
	if err != nil {
		return nil, err
	}
	return session.Directory(ctx)
}

func directoryWeixinWeb(ctx context.Context, scope Scope, private PrivateConnection) (any, error) {
	session, err := NewWeixinWebSession(private.Session)
	if err != nil {
		return nil, err
	}
	if _, err := session.Verify(ctx, scope.Account); err != nil {
		return nil, err
	}
	return session.Directory(ctx)
}

func directoryQq(ctx context.Context, scope Scope, private PrivateConnection) (any, error) {
	session, err := NewQqSession(private.Session)
	if err != nil {
		return nil, err
	}
	if _, err := session.Verify(ctx, scope.Account); err != nil {
		return nil, err
	}
	return session.Directory(ctx)
}

func (service *Service) directoryScope(ctx context.Context, scope Scope, private PrivateConnection, platform string) (any, error) {
	if err := service.Store.RequireRead(scope); err != nil {
		return nil, err
	}
	switch platform {
	case "weixin_local":
		return service.weixin.Directory(ctx, private.Session, scope.Account)
	case "weixin_ilink":
		return directoryILink(ctx, private)
	case "weixin_web":
		return directoryWeixinWeb(ctx, scope, private)
	case "qq_napcat":
		return directoryQq(ctx, scope, private)
	}
	return nil, fmt.Errorf("此平台的原生通讯会话仍在迁移")
}

func (service *Service) scopeAction(ctx context.Context, action string, input map[string]any) (any, error) {
	scope, row, err := service.Store.CheckScope(input)
	if err != nil {
		return nil, err
	}
	if err := service.requireAvailable(row); err != nil {
		return nil, err
	}
	if row.Platform == "weixin_local" {
		if action == "send" {
			return nil, fmt.Errorf("本机个人微信仅支持只读和草稿")
		}
		private, err := service.Store.PrivateState(scope.ConnectionID, scope.Revision)
		if err != nil {
			return nil, err
		}
		switch action {
		case "sync":
			return service.Sync(ctx, scope)
		case "directory":
			return service.directoryScope(ctx, scope, private, row.Platform)
		}
		return nil, fmt.Errorf("此通讯操作尚未完成原生适配：%s", action)
	}
	if row.Platform != "qq_napcat" && row.Platform != "weixin_ilink" && row.Platform != "weixin_web" {
		return nil, fmt.Errorf("此平台的原生通讯会话仍在迁移")
	}
	if row.Platform == "qq_napcat" && row.Fields["mode"] != "manual" {
		if _, err := service.napcat.ensure(ctx); err != nil {
			return nil, err
		}
	}
	private, err := service.Store.PrivateState(scope.ConnectionID, scope.Revision)
	if err != nil {
		return nil, err
	}
	switch action {
	case "send":
		return service.sendScope(ctx, scope, private, row.Platform, input)
	case "sync":
		return service.Sync(ctx, scope)
	case "directory":
		return service.directoryScope(ctx, scope, private, row.Platform)
	}
	return nil, fmt.Errorf("此通讯操作尚未完成原生适配：%s", action)
}
