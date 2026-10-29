# Windows IM 交接文档

更新：2026-10-28  
适用仓库：`EveSunMaple/Zynote`，默认分支 `main`。  
当前状态：P7 已完成，Go 是唯一后端；`src/main` 不再有 TypeScript IM 回退。Windows 的 `weixin_local` 和 NapCat 内置后台仍标记不可用，是唯一剩余功能性迁移缺口。

## 1. 目标

在 Windows 10/11 x64 上，让以下能力全部由 Go/Rust 承担：

- 个人微信本机只读：进程枚举、账号目录识别、内存扫描取密钥、SQLCipher 只读消息库。
- QQ NapCat 内置后台：组件解包、`node.exe` 启动、WebUI/二维码登录、Job Object 生命周期、退出清理。
- 复用现有 `im-connections.sqlite3` 表、加密凭据、session JSON、登录/发送操作记录。
- 不把解密后的微信数据库写到磁盘；日志不含 Cookie、密钥、联系人、消息正文。
- 关闭应用后不残留 NapCat/微信读取进程。

非目标：不写微信数据库、不自动发送本机微信消息、不迁移 Linux NapCat。

## 2. 开始前阅读

1. 仓库根 `AGENTS.md`。
2. 本文件第 5 节验收矩阵。
3. 旧版交接草稿 `docs/tmp/WINDOWS_IM_HANDOFF.md`（如存在，只作历史参考；以本文件为准）。
4. `docs/MIGRATION_CHECKLIST.md` 的 P7、Windows IM、发布相关章节。

## 3. 代码位置

Go：

- `backend/internal/im/service.go`：`availability()` 当前将 `weixin_local` 与内置 QQ 标记不可用。
- `backend/internal/im/store.go`：连接、session、消息、发送回执与加密桥。
- `backend/internal/im/qq.go`：OneBot 与 NapCat WebUI 登录基础。
- `backend/internal/helper/native.go`：Rust 助手 JSON-RPC 客户端。
- `backend/internal/api/research.go`：`tools:request` 的 `im` 入口。

Rust：

- `native/zynote-native/src/main.rs`：`Mode::Wechat` 占位。
- 新增 `native/zynote-native/src/wechat.rs`，Windows-only 实现；非 Windows 返回明确错误。

前端：

- `src/renderer/src/features/communications/Communications.tsx`
- `src/renderer/src/features/communications/ImAutomation.tsx`
- 仅按 capability/status 展示，不要把 Windows 专属逻辑写进 UI。

## 4. 推荐实现顺序

### W1 协议与依赖

- Rust 增加 Windows target 依赖：`windows`、`rusqlite` + `bundled-sqlcipher-vendored-openssl`（仅 `cfg(windows)`）。
- 实现 `wechat.*` JSON-RPC 方法骨架：`processes`、`probe`、`confirm`、`verify`、`directory`、`receive`、`invalidate`。
- 确认 Windows MSVC 工具链：CMake、Perl、NASM、OpenSSL/SQLCipher 构建依赖。
- 用固定 SQLCipher fixture 和伪造 memory reader 做纯逻辑测试；不提交真实账号数据。

### W2 微信本机只读

- 进程枚举：Toolhelp32/QueryFullProcessImageName/GetProcessTimes。
- 内存扫描：VirtualQueryEx/ReadProcessMemory、NtQueryInformationProcess、DuplicateHandle、GetFinalPathNameByHandleW。
- 只读 SQLCipher：`query_only`、内存 temp、只打开 `weixin.exe` 账号库。
- 校验账号目录、文件身份、符号链接和路径；设置扫描/读取/时间预算。
- session 保持 `{reader_id,cursors}`，兼容旧 TS 数据。

### W3 Go 接入

- `WeixinLocal` 接口，Windows 实现 + 非 Windows stub。
- `availability()` 仅在 Windows x64 且 native helper 就绪时启用。
- 接入 `personal-processes/probe/confirm`、`sync`、`directory`；`send` 继续明确拒绝。
- 旧 `im-connections.sqlite3` 连接/消息/加密 session 兼容测试。
- `im:draft` 已由 Go 实现，复用。

### W4 NapCat 托管

- 新增 `backend/internal/im/napcat_windows.go`（`//go:build windows && amd64`）。
- 校验 `resources/knowledge/vendor/NapCat.Shell.Windows.Node.zip` SHA-256。
- 安全解包：拒绝 zip-slip、符号链接、异常大小；暂存目录原子 rename。
- owner 凭据继续走 `crypto.protect-bytes`（domain `im`）。
- 端口分配、`webui.json`、`napcat.json`、`node.exe index.js`、Job Object。
- 复用 `qq.go` 的登录流程；崩溃重启、退出清理、无残留 `node.exe`。

### W5 验收与清理

- Windows 实机跑第 5 节矩阵。
- 删除临时 Windows 专属不可用提示，更新 capability/status。
- 不恢复 TypeScript IM 后端；不做跨平台假启用。
- 跑全量门禁、Windows 打包与 packaged smoke，提交并推送。

## 5. Windows 实机验收

- [ ] Windows 10/11 x64 安装包启动，Go core 握手成功。
- [ ] 本机微信：进程列表、核验、绑定、同步、目录、消息游标、重启恢复。
- [ ] 微信库替换/退出/账号切换时拒绝继续读取。
- [ ] QQ 内置：解包、扫码、绑定、目录、历史、显式发送、取消、退出清理。
- [ ] 旧 TS 连接/消息/加密 session 可读；新写入不损坏旧版可读字段。
- [ ] 崩溃/强杀后无残留 NapCat/微信读取进程；下次启动可恢复。
- [ ] 日志/崩溃报告不含 Cookie、密钥、联系人、消息正文。
- [ ] `tools:request` 的 `im` 全走 Go；macOS/Linux 能力提示正确。
- [ ] Windows 媒体/视频/更新相关 smoke 不回归。

## 6. 安全红线

- 没有 Windows 实机不要把 `weixin_local`/NapCat 标记为可用。
- 不用普通 `modernc.org/sqlite` 打开微信 SQLCipher 库。
- 不提交真实账号、联系人、消息、数据库、密钥或测试凭据。
- 不把解密数据库写入磁盘；不新增明文凭据/会话落盘。
- 不杀用户其它进程；只清理本应用拥有的进程树/Job Object。

## 7. 命令

```sh
pnpm install
pnpm backend:build
pnpm backend:test
pnpm native:test
pnpm verify
pnpm smoke:go-core
pnpm build
cd backend && GOOS=windows GOARCH=amd64 go build ./...
```

Windows 原生构建：

```sh
cargo build --manifest-path native/Cargo.toml --release --target x86_64-pc-windows-msvc
pnpm release lite --platform=win --prepared
pnpm package:smoke --app release/lite/win-unpacked/Zynote.exe
pnpm e2e:packaged --app release/lite/win-unpacked/Zynote.exe
```

缺少 SQLCipher/OpenSSL/CMake/Perl/NASM 时先安装原生构建依赖；不要交叉编译后声称已通过 Windows 实机验收。

## 8. 可复制提示词

```text
你负责在 Windows 10/11 x64 实机上继续完成 Zynote 的 Windows IM 迁移。仓库是 EveSunMaple/Zynote，默认分支 main。开始前先阅读仓库根 AGENTS.md 和 docs/WINDOWS_IM_HANDOFF.md，并检查当前 git 状态和最新提交。

背景：P7 已完成，Go 是唯一后端，TypeScript IM 回退已删除；Windows 的 weixin_local 和 NapCat 内置后台仍标记不可用，是唯一剩余功能缺口。不要在未完成实机验证时把能力标记为可用。

任务：按 handoff 的 W1-W5 顺序实现：
1. Rust --mode wechat：进程枚举、账号目录识别、内存密钥扫描、SQLCipher 只读、session 兼容、预算和路径/文件身份校验。
2. Go WeixinLocal：availability、personal-processes/probe/confirm、sync/directory；send 明确拒绝；旧 im-connections.sqlite3 和 session 兼容。
3. NapCat 托管：zip SHA-256 校验、安全解包、owner 凭据、端口、webhook 配置、node.exe、Job Object、登录/重启/退出清理。
4. Windows 实机验收：旧数据可读、账号切换拒绝、崩溃无残留、日志无敏感信息、能力矩阵正确。
5. 通过后更新 capability/status，跑全量测试、Windows 打包和 packaged smoke。

约束：不提交真实账号/联系人/消息/数据库/密钥；不把解密库写盘；不杀无关进程；不恢复 TypeScript IM 回退；没有实机验证不得声称完成。

门禁：
pnpm verify
pnpm backend:test
pnpm native:test
pnpm smoke:go-core
pnpm build
cd backend && GOOS=windows GOARCH=amd64 go build ./...
pnpm release lite --platform=win --prepared
pnpm package:smoke --app release/lite/win-unpacked/Zynote.exe
pnpm e2e:packaged --app release/lite/win-unpacked/Zynote.exe

完成后提交并推送到 origin/main，提交信息用中文，说明 Windows IM 实机验收结果。如果遇到缺少 Windows SDK/CMake/Perl/NASM/OpenSSL/SQLCipher 工具链，先安装并复现构建；不要跳过验收。
```
