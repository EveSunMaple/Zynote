# Zynote Go 后端迁移清单

> 目标仓库：`git@github.com-eve:EveSunMaple/Zynote.git`
> 上游基线：`EveSunMaple/meiji-listening-guide` @ `main`（0.1.85）
> 文档状态：**定稿 v1.0 / 2026-10-22（决策已全部锁定，P0 已完成）**
> 适用范围：保留 Electron + React 壳，把主进程业务后端迁移到 Go，硬原生模块用 Rust。

## 0. 结论先行

1. **不做一次性重写。** 采用绞杀者模式：Electron 壳保留，Go 后端作为 sidecar 逐步接管，按域切换、可随时回退。
2. **Go 只接管业务。** 窗口、菜单、文件选择器、系统浏览器、`session`、系统钥匙串兼容层留在 Electron；业务规则、数据库、任务、下载、ASR、知识库、IM 全部进 Go。
3. **先冻结契约，再写实现。** 以当前 `src/main/ipc` 的 action 为第一版契约（共 105 个，见 `contract/actions.json`），不先改前端调用方式。
4. **先只读，后写入。** 先迁移列表、详情、设置读取；确认进程、数据库、打包、回滚都可靠后，再迁移写操作。
5. **A4：不留 Node 业务代码。** PDF/ASR/YouTube/微信本地读取用 Rust 重写（`native/zynote-native`，单二进制多 `--mode`）。两个已批准的例外：QQ/NapCat 保持 vendor `node.exe` 原样启动（D1a）；SnapAny 隐藏窗口保留在 Electron 壳（D2a）。
6. **B1：全功能对齐才发版。** 每个域的黄金样本对比通过才能切默认后端；YouTube 对齐口径 = 同画质档位可解析可下载（D3a）。首版 0.0.1（macOS 能力矩阵口径）。
7. **数据迁移必须可回滚。** 切换前备份，首次启动做完整性检查，旧版本至少保留一个可运行版本。

## 1. 迁移边界（全部已锁定）

- [x] 后端语言为 Go（业务核心）+ Rust（原生硬模块）。
- [x] 新仓库 Zynote，远程 `git@github.com-eve:EveSunMaple/Zynote.git`。
- [x] 保留 Electron 壳与 React 19 + HeroUI + Tailwind 前端，只调整 IPC 适配层；不切 Tauri/Flutter。
- [x] macOS 优先（arm64）；Windows/Linux 在各自能力矩阵对齐后另行发布。
- [x] `appId`（`org.listening-assistant.desktop`）与 `userData` 沿用 `listening-assistant`；展示名 Zynote；版本 0.0.1。
- [x] Windows 专属能力（微信只读/NapCat/DPAPI）保留，按平台能力显式禁用；IM 排在 P6、Windows 阶段。
- [x] 许可证维持 AGPL-3.0-only；`NOTICE`/第三方声明保留上游归属（唯一允许保留上游仓库 URL 的位置）。
- [x] 数据目录英文化 + 零 meijiang：新代码一律 `ZYNOTE_*`/`zynote-*`；冻结例外仅 `pdf-annotations.ts`（回退后端字节兼容）与法务归属 URL。

## 1.5 P0 完成记录（2026-10-22）

- [x] 仓库基线：前端/许可/锁文件复制完成；`package.json` 改为 zynote 0.0.1，产品名 Zynote。
- [x] Go module：`backend/`（cmd/zynote-core + internal/{app,transport,contract,api,config,storage}）；stdio JSON-RPC、握手、`app:info`/`settings:get`/`appearance:get`/`identity:get` 只读、`db:health`/`db:backup`（VACUUM INTO）。
- [x] settings:get 的密钥解密经反向 RPC `crypto.unprotect` 走 Electron safeStorage。
- [x] Rust workspace：`native/zynote-native` 多模式桩（pdf/asr/youtube/wechat）。
- [x] Electron 壳：`src/main/backend/go-core.ts`（spawn/握手/崩溃重启 ×3/进程组终止）+ 反向壳服务（`shell-services.ts`）+ IPC 路由（P0 动作走 Go，其余走 TS 回退后端）。
- [x] 契约：`contract/actions.json`（105 个 action，来源/阶段/后端标注）+ P0 结果 Schema 8 份 + fixtures。
- [x] meijiang 清理：`MEIJI_*`→`ZYNOTE_*`、临时目录 `zynote-*`、UA/协议名/分区名、UI 文案、默认人格、mei-avatar 资产删除；冻结例外仅 `pdf-annotations.ts` 与 `NOTICE` 归属 URL。
- [x] 验证：`pnpm verify` ✅、`pnpm backend:test` ✅（含 P0 全链路契约测试）、`pnpm native:test` ✅、`pnpm build` ✅、`pnpm smoke:go-core` ✅（14 项断言）、dev 集成冒烟 ✅（握手 ready）、kill -9 恢复 ✅（新 core 自动拉起）、`ZYNOTE_BACKEND=ts` 切回 ✅（不 spawn core）。
- [ ] 待办：CI workflow；`docs/架构.md` 等上游文档已清 meijiang 字样。
- [x] 打包 POC：`pnpm release lite --prepared` ✅ → `Zynote-0.0.1-lite-arm64.dmg`（274MB）/ `.zip`（289MB）；安装后 `.app` 893MiB（app.asar 353M + unpacked 249M，P7 瘦身目标 −400M）；`bin/zynote-core` 2.9M 已入 Resources 并在打包版中握手成功（本机 Apple Development 证书自动签名）。

## 1.6 P1 完成记录（2026-10-22）

- [x] CI workflow（`.github/workflows/ci.yml`）：pnpm verify + go test + cargo test + build + go-core 冒烟。
- [x] Go 只读域：`course:detail`（五表查询 + chunks_json/question_json 解析，NOT_FOUND/INPUT_INVALID 错误码对齐）、`library:list`（目录遍历 + 课程名映射，FolderName 逐字复刻含 `未命名课程` 兜底）。
- [x] 影子读路由（`src/main/backend/router.ts`）：六个只读 action 双跑对比，键序无关 stable 比较，差异只记键名不记内容。
- [x] 修复三处对齐缺陷：`omitempty` 吞空 `parameters`/`models`；nil slice/map 输出 `null`；dev 提示词根目录 `promptsRoot`（appPath/src）。
- [x] 修复 SQLite 驱动只在测试注册、生产二进制 `sql.Open` 失败的问题（`storage` 包 blank import）。
- [x] 验证：真实 dev 流量下影子读 **零差异事件**（此前抓到 settings:get 的 profiles/prompts 真实差异并修复）；`pnpm verify`/`backend:test`/`native:test`/`build`/smoke 全绿。
- [x] 提交：`de05f9c`（P0 基线）、`4c81a36`（P1 只读域）已推送 origin/main。

## 1.7 P2a 完成记录（2026-10-22）

- [x] 数据目录英文化（TS 回退后端同步）：分类 `Transcripts/Notes/PastExams`、资料根 `Library`、portable `Data`、诊断 `Logs`、笔记文件名 `-notes.md`；`src/main/storage/layout-migration.ts` 启动时自动改名旧中文目录并重定位 DB 内路径（course_files/lectures）。
- [x] Go 写路径：`db:bootstrap`（13 表 schema + ALTER 列检查 + placeholder 会话迁移，逐字对齐 TS Store 构造）、`course:create`（trim/默认会话/ensureLibrary 英文目录/created_at SQLite 格式）、`sessions`、`session:create`（lecture 归属校验 + `· n` 去重命名）；WAL/synchronous=FULL/foreign_keys/busy_timeout 按连接对齐。
- [x] IPC 路由：写 action 走 Go 主路径（TS 不双跑）；删除类（course:delete/session:delete/lecture:delete）仍留 TS——它们依赖 Electron 侧 jobs 状态，待 Go 侧任务管理就绪（P2b）。
- [x] 冒烟扩到 20 项断言全绿；dev 集成影子读零差异；`pnpm verify`/`backend:test`/`build` 全绿。
- [x] 提交：`143e847` 已推送 origin/main。
- [ ] P2b 待办：删除类 action（需 Go 任务状态）、files:add/remove、lecture:create/delete、questions:save/split、导出行为；子目录（Knowledge/MediaDownloads 等）英文化与其记录重定位。

## 1.8 P2b 完成记录（2026-10-22）

- [x] 删除类 action 迁 Go：`course:delete`/`session:delete`/`lecture:delete`（confirmed 二次确认 + 错误文案逐字对齐）；任务前置检查（`jobs.size`）保留在路由层，不依赖反向 RPC。
- [x] `session:delete` 的空课重建走事务化 `createSessionTx`（与 `CreateSession`/`CreateCourse` 共用，避免单连接池死锁），语义对齐 TS（空则重建、取第一课标题）。
- [x] `lecture:detail` 影子读：checkpoint 分段优先 + `toSrt` 逐字复刻（修一处毫秒/秒换算 bug）+ notes/practice 解析 + correction 汇总条件字段。
- [x] 测试新增 2 套（删除路径全生命周期 + 字幕/检查点 detail），Go 全绿；冒烟 20 项断言全绿；dev 集成影子读零差异。
- [x] 提交：`7824045` 已推送 origin/main。
- [ ] P2c 边界：`files:add`/`lecture:create`/`lecture:transcript`/`lecture:courseware` 依赖 `parseCourseware`（PDF/PPTX/DOCX 解析）与 `readTranscript`，归入 P3（Rust PDF 助手）一并处理；`files:remove` 可随 P2c 单独迁。

## 2. 目标架构

```text
React renderer
    │  window.api.invoke / onEvent（尽量不变）
    ▼
preload（contextBridge + 白名单）
    │
    ▼
Electron shell
    │  窗口、菜单、dialog、shell.openPath、BrowserWindow、session、
    │  safeStorage 兼容桥、自动更新、单实例
    │
    │  JSON-RPC 2.0 over stdio（换行分隔 JSON）
    ▼
Go core（业务后端）
    ├── SQLite / 文件库 / 任务队列 / 下载 / 知识库 / IM 编排
    ├── Rust 助手 zynote-native（stdio JSON-RPC，单二进制多 --mode）
    │   ├── --mode pdf      pdfium + lopdf（C2：读 meijiang:/zynote: 双前缀，写 zynote:）
    │   ├── --mode asr      ort + tokenizers + Silero VAD + Whisper
    │   ├── --mode youtube  rustypipe（D3a：同画质档位可解析可下载）
    │   └── --mode wechat   windows-rs + SQLCipher 只读（Windows 专用）
    ├── vendor：NapCat node.exe 原样启动，Go 编排（D1a）
    └── Electron 壳保留 SnapAny 隐藏窗能力（D2a）
```

### 进程与协议规则

- [ ] Go core 由 Electron 用 `child_process.spawn` 启动，不使用 `utilityProcess.fork`（后者只适用于 Node 入口）。
- [ ] Windows 使用 `windowsHide: true`；Unix 使用独立进程组，便于取消整个任务树。
- [ ] **stdout 只用于协议**；Go 的日志、panic 全部写 stderr 或日志文件，避免破坏 JSON 帧。
- [ ] 每帧一个 JSON 对象，最大帧大小有限制（建议 8 MiB）；超过上限的二进制走临时文件/文件句柄，不走 base64。
- [ ] 请求、响应、事件、反向请求都带 `id`；事件带 `jobId`、`sequence`，进度事件可合并，终态结果不可丢。
- [ ] 长任务使用持久 job id + 检查点；取消是协作式 `context.Context`，不是直接杀进程。
- [ ] 写操作尽量保留现有 operation id/幂等键；重试不得产生重复记录。
- [ ] Go core 退出前等待任务检查点落盘；Electron 侧设置超时，超时后按进程树终止。
- [ ] Electron 启动时做协议握手：版本、构建号、平台、数据目录、能力列表；不兼容直接拒绝启动，不静默降级。
- [ ] 单实例：Electron `requestSingleInstanceLock` + Go 数据库租约双保险，禁止两个后端同时写同一数据目录。

### 反向 RPC（Go 调用 Electron）

以下能力建议通过反向请求暴露给 Go，而不是让 Go 直接操作桌面：

- [ ] `shell.pickFiles` / `shell.pickDirectory` / `shell.saveFile`
- [ ] `shell.openPath` / `shell.showItemInFolder`
- [ ] `shell.messageBox`（仅需要系统确认时）
- [ ] `shell.getCookies`（智云、B 站等 Electron session）
- [ ] `shell.openLoginWindow`（智云登录）
- [ ] `crypto.protect` / `crypto.unprotect`（仅限旧 `safeStorage` 密文的过渡期）

## 3. 仓库结构

建议保留前端路径，减少迁移噪声：

```text
Zynote/
├── backend/                     # Go module
│   ├── cmd/
│   │   ├── zynote-core/         # 主入口
│   │   └── zynote-asr/          # 可选：独立 ASR 进程
│   ├── internal/                # 仅服务 backend，不对外暴露
│   │   ├── app/                 # 组合根、依赖注入、生命周期
│   │   ├── transport/           # JSON-RPC、stdio、反向 RPC
│   │   ├── contract/            # action 注册、错误码、事件
│   │   ├── storage/             # SQLite、迁移、备份
│   │   ├── library/             # 资料目录、路径校验
│   │   ├── config/              # settings/identity/appearance
│   │   ├── documents/           # PDF/PPTX/DOCX/导出
│   │   ├── media/               # 下载、认证、ffmpeg、任务
│   │   ├── asr/                 # VAD、Whisper、字幕
│   │   ├── knowledge/           # FTS、向量、分类
│   │   ├── research/            # 研究记录与来源
│   │   ├── learning/            # 字幕、视频、教材、练习
│   │   ├── im/                  # QQ、微信、通讯
│   │   ├── ai/                  # 千问 API、模型路由
│   │   ├── zhiyun/              # 智云课程
│   │   ├── security/            # 系统加密、路径边界
│   │   └── worker/              # 进程监管、租约
│   └── go.mod                   # module github.com/EveSunMaple/Zynote/backend
├── src/
│   ├── main/                    # 仅保留 Electron 壳（窗口/菜单/dialog/session）
│   ├── preload/                 # 桥接层
│   ├── renderer/                # React UI（从旧仓库复制）
│   └── shared/                  # 由 contract 生成的 TS 类型
├── contract/                    # JSON Schema + action/event fixtures
├── resources/                   # ffmpeg、ONNX、NapCat（不提交大文件）
├── scripts/                     # 构建、打包、运行时冒烟
├── docs/
└── package.json
```

规则：

- [ ] `backend/` 是独立 Go module，不把 Go 文件散落在 `src/`。
- [ ] 不允许 Go 包 import Electron；Electron 包也不承载业务规则。
- [ ] 跨域依赖通过 `internal/app` 注入接口，和当前 `ApplicationContext` 的思路一致。
- [ ] 不建 `internal/index.go` 之类的 barrel；Go 用显式包路径。
- [ ] 复制旧代码时保留 `LICENSE`、`NOTICE`、`docs/legal/THIRD_PARTY_NOTICES.md` 与来源说明。

### 工程约定

- [ ] `context.Context` 作为第一个参数；所有阻塞调用可取消。
- [ ] 错误用 `%w` 包装；对外统一映射为稳定错误码，不把底层路径/密钥带进 `message`。
- [ ] 结构化日志用 `log/slog`，带 request id/job id；正文、Cookie、密钥、联系人内容不入日志。
- [ ] 接口定义在消费方；不要为每个包造大而全的 interface。
- [ ] 并发用 `errgroup`/信号量/有界队列；禁止无界 goroutine。
- [ ] 可能触发 native crash 的 ASR、微信内存扫描放独立进程，由 core 监管，不放在主进程。
- [ ] 解析器、路径校验、协议帧用表驱动测试；关键帧/文件格式加 fuzz 测试。

## 4. 契约与类型

当前后端不是薄层：约 105 个 IPC action、6 个 utilityProcess/子进程、多套 SQLite、DPAPI/Keychain 双路径，以及 PDF/PPTX/DOCX/ONNX/YouTube 等重依赖。迁移清单按这个规模设计。

当前 IPC 约有 100+ 个 action，分布在 `src/main/ipc/*.ts`。迁移时不要把 action 名字重命名成 Go 风格，否则前端要全量改。

- [ ] 从旧仓库导出 action 清单：名称、请求字段、响应字段、错误、事件、取消语义。
- [ ] 在 `contract/` 定义 JSON Schema（请求/响应/事件/错误），按域拆文件。
- [ ] 用代码生成 Go struct 和 TS 类型；CI 检查生成结果无 diff。
- [ ] 保留旧 action 名称：`course:create`、`media:create`、`knowledge:search` 等。
- [ ] 统一错误码：`INPUT_INVALID`、`NOT_FOUND`、`CONFLICT`、`BUSY`、`CANCELLED`、`UNAVAILABLE`、`INTERNAL`；同时保留面向用户的中文 `message`。
- [ ] 事件继续复用 `EventPayload` 形状，至少保留 `kind`、`data`、`lectureId`、`stage`、`message`、`error`。
- [ ] 为每个 action 至少一条契约测试；错误路径和取消路径也算。
- [ ] 大文件接口只传路径或临时句柄，不传 base64；路径必须经过库根目录校验。
- [ ] 版本变更走 `protocol` 字段；旧前端连新后端时给出明确“请完整更新”错误。

### 建议的调用形态

```json
{"jsonrpc":"2.0","id":"42","method":"course:detail","params":{"id":"..."}}
{"jsonrpc":"2.0","id":"42","result":{...}}
{"jsonrpc":"2.0","method":"event","params":{"kind":"progress","data":{"stage":"下载","message":"..."}}}
{"jsonrpc":"2.0","id":"42","error":{"code":"INPUT_INVALID","message":"课程不存在","data":{}}}
```

反向请求使用同一通道、相反方向；Go 发 `id`，Electron 回 `result`/`error`。

## 5. 现有后端盘点与 Go 映射

| 当前模块 | 主要职责 | Go 目标 | 阶段 | 风险 |
| --- | --- | --- | --- | --- |
| `src/main/main.ts`、`app/context.ts` | 组合根、生命周期、依赖注入 | `internal/app` | P0 | 中 |
| `src/main/ipc/*` | 100+ action 白名单与参数校验 | `internal/contract` + `internal/api` | P0/P1 | 中 |
| `src/main/storage` | `assistant.db`、portable 迁移 | `internal/storage` | P0 | 高 |
| `src/main/config` | settings/identity/appearance JSON | `internal/config` | P1 | 中 |
| `src/main/library` | 资料目录、文件管理、路径安全 | `internal/library` | P1 | 中 |
| `src/main/documents` | PDF 文本/批注、PPTX、DOCX、导出 | `internal/documents` | P3 | 高 |
| `src/main/media` | 下载、B 站/YouTube、HLS、ffmpeg、任务 | `internal/media/...` | P4 | 极高 |
| `src/main/asr` | Silero VAD + Whisper ONNX | `internal/asr` | P5 | 极高 |
| `src/main/knowledge` | FTS5、向量、分类、引用 | `internal/knowledge` | P5 | 高 |
| `src/main/research` | 研究记录、来源基线、模型桥 | `internal/research` | P5 | 高 |
| `src/main/im` | QQ/NapCat、微信本地/网页/iLink | `internal/im/...` | P6 | 极高 |
| `src/main/learning` | 字幕、视频、教材、练习、工作流 | `internal/learning/...` | P2/P5 | 高 |
| `src/main/ai` | 千问兼容 API、模型路由 | `internal/ai` | P1 | 低 |
| `src/main/zhiyun` | 智云课程抓取/下载 | `internal/zhiyun` + Electron 登录窗 | P4 | 高 |
| `src/main/security` | DPAPI、系统加密、路径校验 | `internal/security` | P0 | 高 |
| `src/main/workers/*` | 6 个 utilityProcess/子进程 | `internal/worker` + 可选 helper | P0 | 高 |
| `src/shared` | 类型、协议、纯函数 | `contract/` + `internal/domain` | P0 | 中 |

### 当前必须保留的行为

- [ ] 不启动 Python、yt-dlp 或 BiliSum 网页服务器（除非明确改变产品约束）。
- [ ] 媒体文件只处理下载到本地的文件；结果时长、画面、音轨、字幕、哈希核验后才清理中间文件。
- [ ] 下载/研究任务有持久检查点；取消/断线后先读状态，不盲目重试未知结果。
- [ ] 知识库来源绑定代际和正文哈希；旧来源只读，不冒充当前来源。
- [ ] 模型凭据只在主进程/系统密钥库中，不进入渲染层、worker 参数或日志。
- [ ] 关闭应用停止所属子进程，不杀用户其它进程。
- [ ] 单实例、路径越界、符号链接、权限、文件大小限制继续生效。

## 6. 分阶段迁移

### P0 基础骨架（必须先完成）

- [ ] 创建 `backend/go.mod`、`cmd/zynote-core`、`internal/app`、`internal/transport`。
- [ ] 实现 stdio JSON-RPC、握手、日志、错误码、取消、事件。
- [ ] 实现 Electron 侧 supervisor：spawn、健康检查、崩溃重启、退出、进程树清理。
- [ ] 实现数据目录解析：`userData`、portable `课程数据`/`资料`、`LISTENING_ASSISTANT_DATA`。
- [ ] 实现 SQLite 打开、WAL、`foreign_keys`、`synchronous=FULL`、`busy_timeout`、`integrity_check`。
- [ ] 实现备份/恢复：首次 Go 启动前复制 `assistant.db`，保留版本号和校验和。
- [ ] 实现 `app:info`、`settings:get`、`appearance:get`、`identity:get` 的只读版本。
- [ ] 打包 POC：Go 二进制进 `extraResources`，三平台能启动并完成握手。
- [ ] 测量启动时间、内存、包体；写入基线。

**退出条件：** 打包后的应用可以启动 Go core，读设置并显示版本；杀掉 core 能自动恢复；旧后端仍可一键切回。

### P1 只读域

- [ ] 课程列表/详情读取；`course:create`/`course:delete` 暂留 TS 后端。
- [ ] `library:list`、`library:open-file` 的元数据读取。
- [ ] 设置、身份、外观、主题的读写兼容。
- [ ] 知识库列表/搜索的只读路径（可暂用旧后端）。
- [ ] 建立 shadow read：同一请求同时走 TS/Go，只记录差异，不影响用户。
- [ ] 把前端 `invoke` 的 transport 做成可切换适配器。

**退出条件：** 只读页面在真实数据副本上无差异；contract 测试全绿；切回 TS 不需要改前端。

### P2 核心写入与学习数据

- [ ] 课程、会话、文件、课次、字幕、笔记、练习、题目。
- [ ] `assistant.db` 全部表结构与迁移逻辑逐字对齐；新增 schema version 表。
- [ ] portable 资料路径重定位、`library-location.json` 兼容。
- [ ] 事务、WAL checkpoint、单写者、并发读。
- [ ] 导出 Markdown/DOCX 的现有行为。
- [ ] 真实数据副本上的创建/删除/恢复演练。

**退出条件：** 课程全生命周期可用；数据库可由旧版本打开（或明确拒绝并保留备份）；无重复写入。

### P3 文档与教材

- [ ] PDF 文本提取、页码/坐标、教材划线。
- [ ] PDF 批注/导出（先做 spike，确认 Go 库能保持现有 PDF 可打开、批注不丢）。
- [ ] PPTX 文本与图片提取、课件解析。
- [ ] DOCX 读取（mammoth 等价）与导出（docx 等价）。
- [ ] 二维码、转写文本、图片 PDF、清理逻辑。
- [ ] 用旧仓库输出做 golden file 对比。

**退出条件：** 同一份课件/教材/试卷，Go 与 TS 输出在可接受差异内；PDF 在 Preview/Adobe/Edge 可打开；失败时保留原文件。

### P4 媒体与智云

- [ ] 下载任务状态机、租约、恢复、取消、成果核验。
- [ ] 统一 HTTP 客户端：超时、重定向白名单、Cookie jar、代理、响应大小上限、TLS 校验。
- [ ] B 站目录、字幕、讨论、登录档案、Cookie 导入。
- [ ] YouTube：先做 Innertube/签名 spike；若 Go 无法稳定，保留 Node helper（见 7.1）。
- [ ] HLS、分片并行、重试、限速、磁盘空间检查。
- [ ] ffmpeg/ffprobe 子进程、mux、字幕封装、超时与进程树。
- [ ] SnapAny：优先评估下线或保留 Electron 隐藏窗口服务，不强行用 Go 重写页面脚本。
- [ ] 智云登录窗口留在 Electron；Cookie/token 通过反向 RPC 或安全通道交给 Go。
- [ ] 下载成果的哈希/时长/轨道/字幕核验。

**退出条件：** 本机固定样本可完整下载、取消、重启恢复；无明文 Cookie 落盘；ffmpeg 失败不留下“成功”记录。

### P5 ASR、知识库、研究

- [x] PCM 提取、Silero VAD、Whisper 分片、时间戳合并（Go `internal/asr` + Rust `--mode asr`；UI 路由见 1.43 待办）。
- [ ] ONNX Runtime Go binding spike（cgo、动态库、macOS 签名、Windows/Linux 分发）。
- [ ] tokenizer/feature extractor 与 `@huggingface/transformers` 输出对齐；否则保留独立 ASR/embedding sidecar。
- [ ] 模型清单 SHA256、路径校验、离线加载、内存预算。
- [ ] FTS5 trigram、向量存储、归一化、分类、引用。
- [ ] 研究来源代际、正文哈希、模型桥、超时与取消。
- [ ] 同一段音频/同一段文本的 golden 对比。

**退出条件：** 字幕时间点、向量维度/归一化、检索结果可解释；模型缺失时明确报错；不联网下载模型。

### P6 IM（可最后做）

- [ ] QQ/NapCat 子进程、登录、消息、发送操作幂等。
- [ ] 微信本地只读（Windows 内存扫描 + SQLCipher 数据库）。
- [ ] 微信网页/iLink、Cookie、二维码、会话。
- [ ] `im-connections.sqlite3` 的表结构、密文格式、迁移。
- [ ] 通讯凭据加密、会话绑定、过期写入拒绝。
- [ ] Windows Job Object/进程组/POSIX 生命周期。
- [ ] 若 Go 版本风险过高，保留 TS IM worker 作为 sidecar，并在协议层隔离。

**退出条件：** Windows 实机通过；其它平台按能力禁用；不泄露联系人/消息/凭据；关闭应用不残留进程。

### P7 切换与清理

- [ ] 默认后端切到 Go，TS 后端只保留一个版本作为回退。
- [ ] 删除 `src/main` 中的业务代码，仅保留 Electron 壳。
- [ ] 清理 `node_modules` 中不再需要的生产依赖。
- [ ] 更新打包配置、签名、公证、自动更新。
- [ ] 数据迁移/回滚演练，至少一次真实备份恢复。
- [ ] 发布 beta，收集崩溃/超时/差异日志。
- [ ] 达到稳定标准后删除 TS 后端和回退开关。

## 7. 高风险子系统决策

### 7.1 YouTube / SnapAny

- 现状：`youtubei.js` 负责 Innertube，`quickjs-emscripten` 执行播放器签名；SnapAny 依赖 Electron 隐藏 `BrowserWindow` 执行页面脚本。
- Go 没有等价的成熟 Innertube 客户端；签名 JS 也不保证能被 Goja 等纯 Go 引擎执行。
- 建议：
  - [ ] 先做 YouTube spike：直接 HTTP + 签名脚本 + Goja/QuickJS binding；失败则保留 Node helper。
  - [ ] SnapAny 不作为核心能力；优先考虑删除或保留 Electron 隐藏窗口服务。
  - [ ] 任何 helper 都必须只暴露 `catalog`/`download` 窄接口，不把整个 Node 后端带回来。

### 7.2 PDF / PPTX / DOCX

- 现状：`pdfjs-dist` 做文本层，`pdf-lib` 做批注，`jszip`/XML 做 PPTX，`mammoth`/`docx` 做 Word。
- Go 候选：`pdfcpu`、`unipdf`（商业许可）、PDFium binding、stdlib `archive/zip` + `encoding/xml`。
- 建议：
  - [ ] 先做“只提取文本/图片”的 Go 实现，满足教材划线主路径。
  - [ ] PDF 批注/导出做保真 spike；不达标就保留窄 Node 文档 helper。
  - [ ] DOCX 生成若只是简单段落，用 zip+XML 模板即可；不要引入不兼容许可证的库。

### 7.3 SQLite / SQLCipher

- 现状：`better-sqlite3`（应用库）+ `better-sqlite3-multiple-ciphers`（微信库）+ FTS5 trigram。
- 建议：
  - [ ] 应用库优先 `modernc.org/sqlite`（纯 Go，便于交叉编译），先验证 FTS5 trigram。
  - [ ] 若 FTS5/性能不达标，改用 `mattn/go-sqlite3`（cgo）。
  - [ ] 微信库需要 SQLCipher 兼容读取；单独 spike，不要假设普通 SQLite 驱动能打开。
  - [ ] 所有库保持单写者、WAL、busy_timeout；迁移脚本幂等。

### 7.4 系统加密

- 现状：Windows 用 DPAPI；macOS/Linux 通过 Electron `safeStorage` 包装；设置、媒体档案、IM 凭据分别有域标记。
- 风险：Electron `safeStorage` 的密文格式不是公开稳定协议，Go 直接解密不可靠。
- 建议：
  - [ ] Windows DPAPI 用 `golang.org/x/sys/windows` 直接兼容旧字节。
  - [ ] macOS/Linux 首次迁移时由 Electron 解密，经本地安全通道交给 Go，再用 Go 自己的信封重新加密。
  - [ ] 过渡期保留 `crypto.unprotect` 反向 RPC；新数据只写 Go 格式。
  - [ ] 禁止明文落盘、日志、崩溃报告；密钥文件权限 0600。

### 7.5 ASR / 向量

- 现状：`@huggingface/transformers` + `onnxruntime-node`，模型清单 SHA256 校验，CPU 4 线程。
- 建议：
  - [ ] 先做 ONNX Runtime Go binding + 固定模型 spike；确认 macOS arm64/Windows x64/Linux x64。
  - [ ] 若 tokenizer/前后处理无法对齐，保留一个独立 ASR/embedding 进程（Go 或 Rust），通过窄协议调用。
  - [ ] 不把模型打进主二进制；继续走 `resources`/组件安装与 SHA256 校验。

## 8. 数据迁移

### 需要盘点的数据

- [ ] `<userData>/assistant.db`：课程、文件、课次、字幕、笔记、练习、消息、会话、检查点。
- [ ] `<userData>/settings.json`、`identity.json`、`appearance.json`。
- [ ] `<userData>/im-connections.sqlite3`：通讯连接、消息、发送/登录操作。
- [ ] `<libraryRoot>/知识库数据/knowledge-native.sqlite`、`research-native.sqlite`、`knowledge.sqlite`（旧库只读）。
- [ ] `<libraryRoot>/媒体下载/`：任务记录、`download-auth/profiles.dpapi`、分段与成果。
- [ ] `<libraryRoot>/字幕同步记录/`、`笔记生成记录/`、`画面识别成果/`。
- [ ] portable：`课程数据/assistant.db`、`课程数据/library-location.json`、`资料/`。
- [ ] `resources/`：ASR/BGE ONNX、FFmpeg/FFprobe、NapCat、runtime manifest。

### 迁移规则

- [ ] 迁移前关闭旧应用，复制整个数据目录；保留原始备份，不在原目录上做不可逆操作。
- [ ] 首次启动执行 `PRAGMA integrity_check`；失败则拒绝写入并提示恢复备份。
- [ ] 迁移在事务中执行；每步可重入；失败回滚到迁移前备份。
- [ ] 记录 `schema_version`、迁移时间、源版本、备份路径。
- [ ] 旧版本打开新库时必须明确拒绝，而不是静默写坏数据。
- [ ] 禁止 TS 与 Go 双写同一 SQLite；切换期间只允许一个后端持有写租约。
- [ ] 文件路径迁移保留相对关系；符号链接、越界路径、大小写差异按旧逻辑处理。
- [ ] 迁移完成后做抽样回读：课程数、课次数、消息数、知识条目数、下载成果哈希。

## 9. 平台与原生能力

- [ ] Windows：DPAPI、Job Object、进程树、微信内存扫描、NapCat `node.exe`。
- [ ] macOS：Keychain 兼容/迁移、签名、公证、`hiddenInset` 标题栏、关闭不退出。
- [ ] Linux：libsecret、无安全后端时拒绝加密写入、`AppImage`/`deb`。
- [ ] FFmpeg/FFprobe：按平台解析，只接受固定工具名，校验路径、权限、符号链接。
- [ ] ONNX Runtime：每平台动态库、cgo、rpath、签名；不允许从网络加载。
- [ ] 能力矩阵：不支持的平台在 UI 和 IPC 双层拒绝，不能只在界面隐藏。
- [ ] 进程清理：Windows Job Object `kill-on-close`；Unix 进程组；不杀无关进程。

## 10. 打包与发布

- [ ] Go 二进制放 `extraResources`（例如 `resources/bin/zynote-core`），按平台构建，不放进 asar。
- [ ] 打包前 `-trimpath -ldflags="-s -w"`；保留崩溃符号的单独构建产物。
- [ ] 只打包目标平台二进制；不要重复当前 `onnxruntime-node` 全平台 + 双份 dylib 的问题。
- [ ] 不打包 `**/*.map`、测试、文档、示例、未使用的前端依赖。
- [ ] 渲染层依赖由 Vite 打包，不再把 React/HeroUI/React Aria 复制进生产 `node_modules`。
- [ ] macOS 对 Go 二进制单独签名并纳入公证；Windows 签名；Linux 校验执行位。
- [ ] 安装包/更新包包含 Go 二进制；升级时旧进程先退出，再替换文件。
- [ ] 版本握手：Electron 版本、Go 构建号、contract 版本、runtime manifest 版本一致。
- [ ] CI 矩阵：Windows x64、macOS arm64（必要时 x64）、Linux x64；cgo 依赖尽量在原生 runner 构建。
- [ ] 产物附 SHA256、第三方许可、SBOM/组件清单。

## 11. 测试与验收

- [ ] `go test ./...`、`go vet`、`golangci-lint`、`govulncheck`、`go test -race` 纳入 CI。
- [ ] 契约测试：每个 action 的正常、参数错误、业务错误、取消。
- [ ] 单元测试：状态机、路径校验、错误映射、序列化、迁移。
- [ ] 集成测试：临时数据目录 + 真实 SQLite + 文件库。
- [ ] Golden 测试：字幕 SRT、笔记/DOCX、PDF 批注、PPTX 文本、知识引用。
- [ ] 媒体测试：本地 HTTP fixture、合成音视频、FFmpeg 生成/探测、断点续传、取消。
- [ ] ASR 测试：固定短音频 + 固定模型，比较时间点与文本（允许容差）。
- [ ] IM 测试：Windows 实机或受控 mock；不把真实联系人数据写入仓库。
- [ ] 安全测试：路径穿越、符号链接、IPC 伪造、日志泄密、Cookie 明文、越权文件读取。
- [ ] 崩溃恢复：kill -9 core、断电模拟、数据库锁、任务检查点恢复。
- [ ] E2E：Playwright 驱动渲染层，对 Go 后端跑主流程。
- [ ] 性能：冷启动、空闲内存、下载吞吐、知识库检索、ASR 实时率。
- [ ] 回归：旧版本数据副本、portable 模式、升级/降级、单实例。

## 12. 切换与回滚

- [ ] IPC 路由层支持 `backend=ts|go` 开关，按域灰度，不要求一次性全切。
- [ ] 只读 shadow 模式：同一请求双跑，记录响应差异；差异率达标后再切写。
- [ ] 先内测/beta，再默认；保留旧安装包和旧后端至少一个版本。
- [ ] 回滚触发条件写清楚：启动失败率、任务失败率、数据差异、崩溃、性能回退。
- [ ] 回滚步骤：停止 Go、恢复旧后端、保留新库备份、必要时恢复旧库。
- [ ] 数据降级策略：新库只增字段时旧版可读；不兼容变更必须显式提示并提供导出/恢复。
- [ ] 达到稳定标准后，再删除 TS 后端和不再需要的 Node 依赖。

## 13. 风险清单

| 风险 | 概率 | 影响 | 缓解 |
| --- | --- | --- | --- |
| Electron `safeStorage` 密文无法在 Go 中兼容 | 高 | 高 | 首次由 Electron 解密并重加密；保留过渡 RPC |
| PDF 批注/导出色差或结构损坏 | 高 | 高 | 先做保真 spike；不达标保留窄 Node helper |
| YouTube/SnapAny 协议变化 | 高 | 中 | 保留 helper；把功能降级为可选项 |
| ASR/embedding 输出与 transformers 不一致 | 高 | 高 | golden 对比；保留独立 sidecar |
| SQLCipher/微信库无法打开 | 中 | 高 | 单独 spike；Windows helper 隔离 |
| Windows Job Object/进程清理不等价 | 中 | 高 | 专项测试；保留原生 helper |
| TS/Go 双写数据库 | 中 | 极高 | 单写租约、迁移期只读、禁止并行启动 |
| cgo 导致交叉编译/签名困难 | 高 | 中 | 原生 runner 构建；纯 Go 优先 |
| 契约漂移 | 中 | 高 | 生成代码 + CI diff + 契约测试 |
| 迁移范围失控 | 高 | 高 | 按阶段门禁；每阶段可独立发布 |

## 14. 第一批任务（建议两周内）

1. [ ] 确认第 1 节“需要确认”的 5 个决策。
2. [ ] 从旧仓库复制前端、`LICENSE`/`NOTICE`/第三方声明，建立 Zynote 目录。
3. [ ] 创建 `backend/go.mod` 与 `cmd/zynote-core`，输出 `hello` 握手。
4. [ ] 实现 Electron supervisor + stdio JSON-RPC + 日志/退出/崩溃重启。
5. [ ] 实现 `app:info`、`settings:get`、`appearance:get`、`identity:get` 只读接口。
6. [ ] 实现 SQLite 打开、备份、完整性检查，不迁移业务表。
7. [ ] 建立 `contract/` 的 action 清单与第一条契约测试。
8. [ ] 打包一个只含 Go core 的三平台 POC，记录包体/启动/内存。
9. [ ] 写 shadow read 适配器，验证前端可切换后端。
10. [ ] 用真实数据副本做一次“启动 Go -> 读取 -> 退出 -> 回滚旧版”演练。

## 15. 完成定义（全量迁移）

- [ ] 所有业务 action 由 Go 或明确的窄 sidecar 提供，Electron 主进程不再含业务规则。
- [ ] 旧 TS 后端删除；保留的 Node/Rust helper 有协议、负责人、淘汰期限。
- [ ] 旧版本数据可自动迁移，失败可回滚；portable 模式通过。
- [ ] Windows/macOS/Linux 验证矩阵通过；平台不支持项有明确提示。
- [ ] 契约、Golden、E2E、崩溃恢复、安全测试全部通过。
- [ ] 安装包体积、启动时间、内存、下载/ASR 性能达到基线。
- [ ] 许可证、第三方声明、SBOM、签名/公证完成。
- [ ] 发布 beta 并完成至少一次真实回滚演练。

## 1.9 P3a/P3b/P3c 完成记录（2026-10-22）

- [x] **P3a PDF 文本提取**（`0cda4f5`）：`zynote-native --mode pdf`（pdfium-render + `resources/pdfium/libpdfium.dylib`，gitignore 大文件）；Go 桥接 `internal/helper`（spawn/管道/崩溃处理/按 id 关联修复 base64 二次编码 bug）；`pdf.text`/`pdf.plainText` 全链路（Electron→Go→Rust→pdfium）；黄金对比：两页文本逐行一致，坐标偏差 x≤0.8pt / y≤1.8pt / w≤1.3pt / h≤3.3pt（pdfjs baseline vs pdfium bounds 语义差，容差口径已记录）。
- [x] **P3b 批注 C2**（`b77c6f0`）：lopdf 批注读写；inspect 读双前缀（`meijiang:`+`MeijiangData` / `zynote:`+`ZynoteData`）；write 全量重放语义对齐 TS（旧批注需在 rows 带上 → 迁移为 `zynote:`，T='Zynote'）；保存后回读校验（pages/count/逐行 rect 容差 0.00001）；跨语言 round-trip 测试（pdf-lib 写旧格式 → Rust 读 → Rust 写 → pdf-lib 验证）。
- [x] **P3c 文档解析**（`a21604f`）：Go `internal/documents`——`ParseSrt`/`milliseconds`（错误文案逐字对齐）、`SplitTranscript`（时间戳/估算时长/递增校验）、`ParseDocx`（zip+xml，提升 TS fallback 逻辑为主路径）、文本编码检测（chardet + GB18030/UTF-8 策略对齐）；`document:parse`/`transcript:read` 接入。
- [x] 验证：Go 测试（documents 包新增 4 套）、冒烟 26 项断言、parity、roundtrip、build 全绿。
- [ ] **P3d 待办**：`parseCourseware` PPTX 幻灯片文本（jszip+saxes → Go zip+xml）与序列化缓存 `serializeCoursewareSourceCache`；`files:add`/`lecture:create`/`lecture:transcript`/`lecture:courseware` 完整链路（copyInto + readTranscript + parseCourseware 组合）。

## 1.10 P3d/P3e 完成记录（2026-10-22）

- [x] **P3d**（`04ecca3`）：`internal/courseware`（Chunks 14k 分块/缓存序列化/stale 判定，RE2 无 lookahead 手动分割）；`ParsePptx`（页面目录/rels/文本截断/图片预算/omissions 逐字对齐）；`files:add`/`files:remove` 链路（ManagedFile 修复 `Join` vs `resolve` 绝对路径语义 + /var symlink 边界）；`importDocument` 语义修正（text 为原始解析文本，不包缓存）。
- [x] **P3e**（`6270ff7`）：`lecture:create`（resolveSource→readTranscript→parseCourseware→needsOcr 拒绝→PPT/Transcripts 双 copyInto→缓存序列化→CreateLecture+占位会话改名→SetLectureTranscript）；`lecture:transcript`/`lecture:courseware`（导入即清派生表 + 返回 lectureDetail）；`questions:save`（归属校验/删除清理/practice_sets 复位，错误文案逐字对齐）；字段名对齐 TS（transcript/courseware 用 `path`，create 用 `transcriptPath`）。
- [x] 验证：Go 测试（lecture 链路含占位会话改名/缓存标记/外键/越界）、冒烟 30 项断言、build 全绿。
- [x] **迁移进度：28/105 action 已由 Go+Rust 承担**。
- [ ] **P4 待办（最大硬骨头）**：媒体下载域——B 站目录与登录档案、ffmpeg mux、YouTube（Rust rustypipe）、智云导出链路。

## 1.11 P4b/P4c 完成记录（2026-10-22）

- [x] **P4b 传输层**（`a6c9be3`）：`transport.go`（代理/IPv4/超时/重定向拒绝/sleep-requests 节流/rate 限速）；`transfer.go`（journal 断点续传：200/206/416、ETag 强校验、`.lock` 互斥、取消保留 checkpoint、写入上限）；`artifact.go`（成果预留/核验/`.incomplete-` 归档）；测试覆盖全路径。
- [x] **P4b 并行与 HLS**（`e3eb7e7`）：`parallel.go`（HEAD 探测 → 分片 → 有界并发 → 合并；服务器忽略 Range 自动回退串行）；`hls.go`（EXTM3U 严格解析：变体/音轨/密钥/AES-128-CBC 解密/直播拒绝；原生分段下载 + artifact 合并）；本地 httptest 测试含加密流。
- [x] **P4c 编排基础**（`0b8ae0c`）：`tools.go`（app-owned 可执行解析、目录/符号链接/越界校验、系统白名单回退）；`command.go`（进程组取消、输出上限、退出码映射）；`mux.go`（ffprobe 探测：时长/轨道/封面/章节）；`service.go`（options/list/retry/cancel/file 投影）；反向加密桥 `crypto.protect-bytes`/`unprotect-bytes`（复用 `systemProtect` 信封，与 TS 兼容）；媒体目录英文化 `媒体下载`→`MediaDownloads`（TS+Go+启动迁移）。
- [x] 过程中修复：Initialize 每次重扫把新建 queued 任务误标 interrupted（改为 memoized）；根目录不存在时 EvalSymlinks 未解析导致目录校验失败；artifact 复用未记录 owner；transfer Progress 空指针；代理 scheme 未校验。
- [x] 验证：Go 测试 3 包全绿（media 新增 tools/command/probe/service/并行/HLS 套件）、冒烟 **33 项断言**、`pnpm verify`/`build` 全绿。
- [x] **迁移进度：33 个能力由 Go+Rust 承担**（105 个 IPC action 中约 30 个已切换）。
- [x] **P4c-2c 目录解析与流选择**（`3ea6560` `24e5b50`）：`video-policy`（`VideoURL`/`VideoMediaURL`/`MediaAddress` 域与协议白名单）；`BiliMediaCatalog.Read`（单视频/合集、分集唯一性、DASH 流 + durl 回退、字幕/章节/封面、format_ids 校验）；`SelectStreams`（精确格式、音质上限、编码前缀、组合回退）；`media:catalog` 接入。
- [ ] **P4c 剩余**：下载 runner + `media:create`（需 ffmpeg mux、sidecars/layout/filenames/sponsorblock 子模块）；`media:discussion*`；媒体 legacy 记录只读合并。

## 1.12 P4c-2a/2b 完成记录（2026-10-23）

- [x] **P4c-2a 登录档案**（`4a212db`）：`internal/media/auth.go` —— Netscape Cookie 严格解析（`#HttpOnly_`、域归属、去重、过期过滤、1MB/2000 条上限、错误文案逐字对齐）、站点域校验（拒 co.uk/com.cn/com.au）、加密档案（`download-auth/profiles.dpapi`、8MB 上限、原子写入、50 档案上限）、`Scoped` Cookie 注入（https/无端口/域归属、剥离 cookie/authorization、http 不注入）；`media:auth-import/list/remove` 接入 + `shell.pickFile` 反向桥（Electron dialog 保持壳内）。
- [x] **P4c-2b B 站资源客户端**（`42de1d5`）：`internal/media/bilibili` —— WBI 签名（mixin key 表、过滤 `!'()*`、md5、1h 缓存）、`read` 错误码映射（-101 登录失效）、`status/browse`（搜索/热门、HTML 清洗、BV 校验、分页）、QR 登录状态机（generate/poll：0/86101/86090/86038，180s 过期，官方 origin 校验）、`folders/videos`（账号归属校验、失效视频标记）、Cookie 加密持久化（自定义存储：域/路径/过期/删除）；QR PNG data-URL（skip2/go-qrcode，scale 5）；7 个 `bili-resources:*` action 接入。
- [x] 契约清单修正：`contract/actions.json` 补录带连字符域名的 7 个 action，总数 105 → **112**。
- [x] 验证：Go 4 包测试全绿（bilibili 新增 6 套：WBI/错误码/搜索/QR/收藏夹/Cookie 持久化）、冒烟 **34 项断言**、verify/build 绿。
- [x] **迁移进度：43 个能力 Go 化**（112 个 action 中用户可见约 31 个）。
- [ ] **P4c 剩余**：`media:catalog/create/discussion*`（需 media-bili 目录解析 + 下载 runner + ffmpeg mux）；媒体 legacy 记录只读合并。
- [ ] **P4d**：YouTube（Rust `--mode youtube`，rustypipe spike + D3a 口径对比）；智云 `zhiyun:search/lessons/export`；SnapAny 留 Electron 壳。

## 17. 已决事项记录

以下问题已全部拍板（原"待确认问题"清单）：

1. 平台节奏：macOS 优先（arm64），Windows/Linux 按各自能力矩阵对齐后发布。
2. 产品名：Zynote；`appId`（`org.listening-assistant.desktop`）与 `userData` 沿用。
3. Node helper：不留（A4）——PDF/ASR/YouTube/微信用 Rust 重写；QQ/NapCat 保留 vendor 二进制（D1a）、SnapAny 留 Electron 壳（D2a）。
4. 对齐口径：B1 全功能对齐；YouTube = 同画质档位可解析可下载（D3a）。
5. Windows 专属能力：保留，按平台能力显式禁用。
6. 数据迁移：首次启动自动改名旧中文目录 + DB 路径重定位（已实现）。
7. 许可证：维持 AGPL-3.0-only。
8. 版本：0.0.1 测试版。

## 1.13 P4c-2c/2d/2e/2f 完成记录（2026-10-23）

- [x] **P4c-2c 目录解析与选流**（`3ea6560` `24e5b50`）：`video-policy`（`VideoURL`/`VideoMediaURL`/`MediaAddress` 协议与域白名单）、`catalog.go`（单视频/合集、分集唯一性、DASH 流 + durl 回退、字幕/章节/封面、format_ids 校验）、`SelectStreams`（精确格式、音质上限、编码前缀、组合回退）；`media:catalog` 接入并返回 15 分钟 token（200 上限）。
- [x] **P4c-2d 文件名/布局/侧车**（`1f5ad7a`）：`naming.go`（模板字段、100 字节截断、非法字符、restrict 模式）、`layout.go`（source/type/date 目录、exclusive 复制 + 哈希核验、cleanup 语义）、`sidecars.go`（O_EXCL、描述/链接/mtime、allowedTuning 白名单）。
- [x] **P4c-2e 时间线与 mux**（`f81e656`）：`timeline.go`（区间校验/保留时间线/章节重映射）、`mux_full.go`（完整 Mux/PrepareCover/Concatenate、章节+片段、预期时长核验、封面嵌入、假 ffmpeg/ffprobe 离线测试）。
- [x] **P4c-2f 下载 runner + `media:create`**（本次）：`download/runner.go`（catalog 复核身份→封面→选流→串行/分片/HLS→拼接→mux→拆分章节→发布→Evidence→清理；错误/取消状态机；aria2c 降级警告；字幕/SponsorBlock 显式拒绝）；`download/service.go`（单活动队列、取消、进度内存投影）；`media:create`（token 重读复核身份、skip_existing 去重、入队）+ `media:cancel` 取消活动任务；hello 增加 `packaged` 参数（决定系统工具回退）。
- [x] 修复：`jobs.Create` 误用 32 位十六进制 ID 正则校验 identity（会拒绝真实 B 站分集标识），改为 1..500 字符；Transfer 警告改为追加而非覆盖。
- [x] 验证：`go vet`、`go test ./...`（download 新增 4 套：成功发布/字幕拒绝/章节拆分拒绝/身份变化）、`pnpm smoke:go-core`（45 能力）、`pnpm verify`、`pnpm build` 全绿。
- [x] **迁移进度：45 个能力 Go 化**（112 个 action）。
- [ ] **P4c 剩余**：`media:discussion`/`media:discussion-import`（B 站评论抓取 + 选择导入）；媒体 legacy 记录只读合并；路由层 media 域灰度切换（当前 Go 能力尚未全部加入 `goPrimaryActions`/影子读集合）。
- [ ] **P4d**：YouTube（Rust `--mode youtube`，rustypipe spike + D3a 口径对比）；智云 `zhiyun:search/lessons/export`；SnapAny 留 Electron 壳。

## 1.14 P4c-2g 讨论证据完成记录（2026-10-23）

- [x] **讨论采集**（本次）：`bilibili/discussion.go`（标签启发式、canonical JSON/digest、证据渲染）+ `discussion_client.go`（视频信息校验、评论热/新两轮 + WBI 首页回退、子评论有界补齐、弹幕 protobuf/XML 解析、分段/回退/去重/排序、有界覆盖回执）+ `discussion_store.go`（`cache/video-discussion/evidence.sqlite3`，snapshots/reviews/heads 三表，IMMEDIATE 事务，ref 校验与选择重放）。
- [x] `Resources.ReadDiscussion`（路径白名单 + WBI 签名 + context 超时）与 `RawDiscussion`（弹幕原始字节，12MB 上限）。
- [x] `media:discussion` 接入（pages 1–10，默认 2）；capabilities 45 → **46**。
- [x] 测试：分类/标签顺序、canonical JSON 无 HTML 转义、渲染根上下文、XML/protobuf 弹幕解析、证据库保存/选择/旧 ref 拒绝/复核合并、httptest 端到端采集。
- [ ] **剩余**：`media:discussion-import`（依赖 P5 knowledge `import`，待知识库域迁移后接入）；媒体 legacy 记录只读合并；路由层 media 域灰度切换。

## 1.15 媒体 legacy 只读合并（2026-10-23）

- [x] `media/download/legacy.go`：只读视图（`records/<kbr>/<id>/state.json`），scope 计算与 Python/TS 一致（realpath + 小写 + `kbr_` 前缀）；目录/记录/清单均拒绝符号链接、越界与超限。
- [x] `media:list` 合并旧记录（同 id 以原生队列为准；queued/running 投影为 interrupted，读取失败记录为只读 error 行）；`media:file` 原生优先、旧成果回退。
- [x] `media:retry` 旧记录：completed 先核验成果；未完成先复核站点身份（`bvid/cid/page/storage_id` 一致）→ catalog 复核 → `unchanged` 记录哈希 → 以同一 id 建原生任务并入队；精确格式/登录档案等不可迁移选项显式拒绝。
- [x] `media:create` 的 `skip_existing` 同时匹配旧完成任务（身份 + 规范化 options + 成果核验）。
- [x] 修复：原生 `media:retry` 未入队（此前 Go 重试只改状态）；旧记录 scope 未统一小写。
- [x] 测试：legacy 列表/文件/recipe、interrupted 与不可读行、未完成分段拒绝、精确格式拒绝、跨 scope 冲突；Go 全量测试与 vet 通过。

## 1.16 媒体域路由切换与讨论跨语言校验（2026-10-23）

- [x] 路由：`media:catalog/create/retry/cancel/discussion/auth-import/auth-remove` 与 `bili-resources:browse/folders/videos/qr/poll/logout` 进入 Go 主路径；`media:options/list/auth-list`、`bili-resources:status` 保持影子读；`media:discussion-import`（依赖 P5 knowledge）与 `media:open`（Electron shell）继续走 TS。
- [x] 修复：`media:retry` 原生任务此前只切状态不入队，现按 TS 语义先查活动任务、转 queued 后入队。
- [x] 跨语言证据：`scripts/discussion-roundtrip.mts`（经 `ELECTRON_RUN_AS_NODE` 使用 Electron ABI 的 better-sqlite3）验证 canonical JSON/digest/packRef 与 SQLite 表结构在 Go↔TS 双向一致：Go 写 → TS select；TS 写 → Go select。新增 `pnpm parity:discussion`。
- [x] 验证：`go test ./...`、`pnpm verify`、`pnpm build`、`pnpm smoke:go-core`、`pnpm parity:discussion` 全绿。

## 1.17 P4d 平台目录基础（2026-10-23）

- [x] `media/source.go`：`Catalog/MediaStream/SubtitleTrack/Chapter/Source` 提升为平台中立类型，`bilibili` 保留同名别名（零改动测试）。
- [x] `internal/media/platform`：`Input`（BV/bilibili/YouTube/Vimeo/Dailymotion 网址识别与规范化，含播放列表/哈希歧义校验）+ Vimeo/Dailymotion 原生目录（域名白名单、进度/封面/字幕、格式校验、HLS 回退）。
- [x] 测试：输入识别、Vimeo 目录/身份不一致/字幕、Dailymotion 目录、播放列表拒绝、format_ids 变化。
- [ ] **媒体路由暂缓**（`dd501fe`）：Go 尚未覆盖 YouTube（youtubei.js + quickjs 签名），为避免下载回归 media/bili-resources 继续走 TS。
- [ ] **下一步**：Rust `--mode youtube`（rustypipe spike，若签名不可用按 D3a 重新评估）；接入平台 dispatcher（catalog/create/runner 共用）；再恢复媒体域路由切换。

## 1.18 打包补齐 PDFium（2026-10-23）

- [x] `package.json` `extraResources` 增加 `resources/pdfium` → `pdfium/`（dylib/dll/so 过滤）；此前打包版 `pdf.text` 会因缺少动态库失败。
- [x] Go 按 `runtime.GOOS` 选择 `libpdfium.dylib`/`pdfium.dll`/`libpdfium.so`，不再硬编码 macOS 文件名。
- [x] `resources/pdfium/README.md` 记录获取方式与命名（二进制仍不入库）；`docs/legal/THIRD_PARTY_NOTICES.md` 补 PDFium BSD-3-Clause 全文与来源。

## 1.19 Rust 单元测试（2026-10-23）

- [x] `annotations.rs` 测试：UTF-16BE BOM 往返（中文/emoji/控制字符）、无 BOM UTF-8 回退、双前缀识别、批注字典键（NM/T/Contents/ZynoteData），以及 lopdf 最小 PDF 上的写入→回读端到端（页数/数量/坐标/描述）。
- [x] `pnpm native:test` 由 0 个测试变为 5 个通过；crates.io 当前不可达，YouTube rustypipe 迁移待网络恢复后继续。

## 1.20 平台 dispatcher（2026-10-23）

- [x] `platform.Dispatcher`：`media:catalog` 与 `media:create` 共用 `readCatalogSource`（BV/B站 → Bili catalog；Vimeo/Dailymotion → 平台目录 + 登录档案 scoped fetcher）；runner 执行前通过 `ReadWithFetcher` 复核平台目录。
- [x] 证据令牌保存平台中立的 `media.Source`；`media:create` 重读时按同一 dispatcher 校验身份。
- [ ] **已知缺口**：B 站 `cookie_profile` 的目录读取仍用内置 Cookie 存储，未像 TS 一样用档案 scoped 的 BiliResources；待媒体路由恢复前补齐。
- [ ] **YouTube**：crates.io 不可达（rustypipe 无法拉取），`platform.Read` 暂显式报错；媒体路由保持 TS。

## 1.21 Go 字幕下载与嵌入（2026-10-23）

- [x] `media/subtitles.go`：语言模式匹配（含 `-` 排除）、WebVTT / B站 JSON / YouTube json3 解析、时间/正文校验、SponsorBlock 保留区间重映射（`RetimeCues`）、SRT/VTT/LRC/ASS/original 输出、`subtitle_only` 与 `embed_subtitles` 写入（内容相同可重入、不同则保留原文件报错）。
- [x] runner：移除字幕拒绝；字幕下载（自动字幕警告）、嵌入 mux、仅字幕发布分支、清理 embed/未保留字幕；Vimeo/Dailymotion 轨道标记 `webvtt`，B 站保持 JSON。
- [x] 测试：语言匹配、VTT/JSON/json3 解析、四种格式文本、时间线重映射、下载/幂等/冲突/自动字幕过滤/重复语言、runner 仅字幕端到端。
- [ ] **仍缺**：SponsorBlock 片段移除（TS 支持，Go 显式拒绝）；媒体路由继续 TS 直至 YouTube 迁移。

## 1.22 B 站 scoped 目录（cookie_profile）与 YouTube 探测（2026-10-23）

- [x] `bilibili.NewScopedResources(fetcher)`：目录读取走登录档案 scoped fetcher（nil Cookie 存储安全），WBI playurl 使用同一 fetcher 签名；dispatcher 与 `media:create` 重读在 `cookie_profile` 下使用 scoped catalog。
- [x] 测试：假 fetcher 覆盖 nav（WBI 密钥）/view/playurl/player，断言请求带 `w_rid`/`wts` 且返回 `media.Source`。
- [x] YouTube 网络探测：crates.io SSL 超时；直接 InnerTube（WEB legacy key）返回 `UNPLAYABLE`，无签名/会话不可用，确认仍需 rustypipe + 播放器签名，维持 TS 路由。

## 1.23 运行时冒烟修复（2026-10-23）

- [x] `scripts/runtime-smoke.mts` 的设置分区标签更新为当前 UI（搜索服务/提示词库/模型服务），修复自 UI 改名后一直失败的导航阶段。
- [x] `pnpm runtime:smoke` 全绿：seed/restart（Electron safeStorage、生产媒体/IM 加密二进制、Cookie/IM 跨重启、加密日志恢复）+ 设置导航/窗口激活复验。

## 1.24 Go SponsorBlock 片段移除（2026-10-23）

- [x] `media/sponsorblock.go`：仅 YouTube、分类去重排序与白名单、20s 超时、1MB 上限、404 视为无标注、身份重复/时长不符/恢复记录篡改拒绝；回执 `sponsorblock-plan.json`（严格字段、sha256、CC BY-NC-SA 归属）复用。
- [x] runner：Plan 提示与警告、`KeepSegments` 传入 mux、字幕按保留区间重映射（空/过多拒绝）；移除原“未接入”拒绝。
- [x] 测试：正常移除/过滤非 skip/缓存命中零网络/篡改拒绝、404、平台限制、分类白名单、时长不符。
- [ ] SponsorBlock 只能在 YouTube 迁移完成后端到端验证。

## 1.25 YouTube 原生目录（Rust rustypipe，2026-10-23）

- [x] `native/zynote-native` 增加 rustypipe 0.11.4 + tokio；`--mode youtube` 支持 `youtube.video`（标题/时长/封面/描述/格式/字幕，DRM 与直播拒绝，itag 去重，`fmt=json3` 字幕），缓存目录由 Go 传入 `--youtube-cache`。
- [x] Go：`helper.Native` 支持 youtube 模式与缓存参数（`cmd` 创建第二个助手）；`platform` 增加 `NativeCaller` 与 YouTube video/playlist 分支；dispatcher/`readCatalogSource` 接入；`media:catalog` 实测返回 27 个格式、720/1080/2160 等档位。
- [x] 依赖合规：NOTICE 记录 rustypipe GPL-3.0（与 AGPL-3.0-only 组合）。
- [x] 稳定性：自生成 visitor data（避免 rustypipe 首次拉取 visitor data 失败 panic）+ `catch_unwind` 将内部 panic 转为错误；缓存目录由核心传入。
- [x] 传输层修复：默认遵循系统代理（`ProxyFromEnvironment`），移除 `http.Client.Timeout` 总时长限制（改为连接/响应头超时），避免大文件被 60s 总超时截断。
- [x] E2E：Go core → `media:catalog` 实测 27 格式/8 档位；下载尝试受本机代理吞吐（约 3.7KB/s，curl 同样截断）阻塞，未完成成品验证。
- [x] **播放列表**已由 Go InnerTube 补位（见 1.26）；**剩余**：翻译字幕未暴露，媒体路由保持 TS。

## 1.26 YouTube 播放列表（Go InnerTube，2026-10-23）

- [x] `platform/youtube.go`：自生成 visitor data，WEB InnerTube `browse` 拉取播放列表；兼容新 `lockupViewModel` 与旧 `playlistVideoRenderer` 布局，标题/条目去重、continuation 最多 10 页或 100 条；`limited` 标记。
- [x] 实测：`PLFgquLnL59alCl_2TQvOiD5Vgm1hCaGSI` 返回 100 条唯一视频，`limited=true`；单测覆盖两页 continuation、旧布局、空列表。
- [ ] **剩余 YouTube 缺口**：翻译字幕（rustypipe 未暴露 translationLanguages）；媒体路由仍待该缺口决定。

## 1.27 外观与身份写路径迁移（2026-10-23）

- [x] `config.SaveIdentity` / `SaveAppearance` / `ClearAppearance`：原子写入、字段校验、课程存在性检查；`appearance:save/clear`、`identity:save` 切 Go 主路径。
- [x] capabilities 46 → **49**；路由 `appearance:save/clear`、`identity:save` 仅走 Go。
- [x] 测试：身份 trim/头像格式/空名拒绝、外观全局+课程合并/清除/非法值；Go 全量测试、smoke（49）、verify/build 全绿。

## 1.28 反向 RPC 死锁与 B 站 CDN 兼容（2026-10-23）

- [x] **修复反向 RPC 死锁**：stdio 请求处理阻塞回复帧读取，`crypto.*`/`shell.pickFile` 会等满超时；请求改为串行 goroutine 执行，Serve 循环持续消费回复帧（新增 transport 测试）。
- [x] B 站 playurl 的备份 CDN 可能带 `:8082` 端口（不可信白名单），现过滤无效备份地址、保留有效地址；无有效地址才拒绝。
- [x] **端到端**：Go core 实下 B 站 `BV1GJ411x7h7`（360p HEVC + AAC），9.5MB `result.mkv`，ffprobe 时长 212.3s，成果核验完成。

## 1.29 P5 知识库前半（2026-10-23）

- [x] `internal/knowledge`：与 TS 完全一致的 `knowledge-native.sqlite` schema（sources/chunks/migrations/vectors/trigram FTS + 触发器）；导入（900/1100 分块、证据 id `evi_`+sha、原子发布）、列表/阅读/generation、关键词检索（FTS 长词 + instr 短词/中文 n-gram、按来源多样化、引用校验）、分类草稿读写、整理/删除/重建。
- [x] 12 个 action 接入：`knowledge:list/generation/read/import/citation/delete/organize/rebuild/search/classification-options/classification-read/classification-save`；capabilities 49 → **61**。
- [x] 跨语言：`pnpm parity:knowledge`（Electron ABI + esbuild 打包 TS store）验证 Go↔TS 双向导入/阅读/检索/引用，schema 与证据 digest 完全兼容。
- [x] 测试：导入/分块统计、幂等与 expectedGeneration、FTS/中文检索/作用域、引用篡改拒绝、整理/删除/重建、分类草稿与非法选项、app 级 action 链路。
- [ ] **剩余**：`knowledge:ask`/`classification-suggest`（AI）、`vector-*`（Rust ORT + bge 模型）、`tools:*`/`im:draft`；旧 Python 知识库 baseline 迁移；根目录仍为 `知识库数据`（随 P5 改名）。

## 1.30 M1 AI 客户端与聊天（2026-10-23）

- [x] `internal/ai`：chat_completions/responses 双格式、SSE 严格完成标记、180s 超时、模型选择（视觉/文本）、参数（温度/TopP/最大输出/推理强度）、错误提示与 API 状态分类；`Models` 列表。
- [x] `config`：`ActiveProfile`/`DraftProfile`（Base URL 规范化、同地址复用加密 Key、地址变更必须重填）、`CacheModels` 合并模型缓存、`IdentityPrompt` 与 TS 文案一致。
- [x] `chat`：用户消息先落库、流式 `kind:chat` 事件、助手消息落库、`kind:job` 完成/失败与 `任务停止` 进度事件；`cancel` 取消活动任务；`job:current` 返回当前任务。
- [x] `settings:models/probe/test` 接入 Go；capabilities 61 → **67**。
- [x] stdio 请求改为并发处理（反向 RPC 回复不再被长任务阻塞；SQLite 由 busy_timeout/WAL 串行化），新增 jobs 互斥/取消测试。
- [x] 测试：SSE 完成/截断、JSON/Responses、模型列表、聊天事件与消息持久化、模型草稿校验/Key 复用/缓存写入。
- [ ] **未切路由**：chat 与模型测试仍走 TS，待界面手测后加入主路径。

## 1.31 M2 知识库问答与分类建议（2026-10-23）

- [x] `ai.JSON`：结构化输出两次尝试、围栏清理、校验失败反馈与 1.5s 重试。
- [x] `knowledge:ask`：检索 → 片段 JSON → 引用逐字核验（≥6 字、命中来源）→ 重新 citation 校验 digest/generation；无命中返回固定 limitation。
- [x] `knowledge:classification-suggest`：分类草稿 + 选项 → 结构化选择校验。
- [x] 测试：检索-问答-引用、分类建议、无命中；capabilities 67 → **69**。
- [ ] `tools:request`/`tools:skill-open` 仍依赖研究后端（约 3.4k 行），留待研究域迁移。

## 1.32 M4 智云 search/lessons（2026-10-23）

- [x] 壳侧 `shell.zhiyunSession` 反向服务：读取 `persist:zhiyun` Cookie 与 `_token` Authorization；Go `internal/zhiyun` 客户端（30s、系统代理、网络重试、401/403/5002 登录提示、登录页/JSON 异常分类）。
- [x] `zhiyun:search`：infosimple 账号校验 → searchlist（页码/每页 20/去重/过滤 recommend/hasMore）。
- [x] `zhiyun:lessons`：catalogue 权限与结构校验、content JSON 解析、`videoAvailable` 标记（不暴露 URL）。
- [x] 测试：搜索映射/去重/分页、课次内容、Cookie/Authorization 透传、登录/权限/HTML 错误；capabilities 69 → **71**。
- [ ] `zhiyun:export` 仍留 TS（依赖视频 PPT 管线/文件落盘）。

## 1.33 media:discussion-import（2026-10-23）

- [x] `media:discussion-import`：讨论 ref 选择 → 证据文本 → 知识库导入（expectedGeneration 防覆盖）→ `Notes/<分类>/<标题>-<ref>.md` 写入并回读核验。
- [x] 测试：证据选择/备注渲染、默认分类、笔记文件与知识库列表；capabilities 71 → **72**。

## 1.34 library:delete 迁移 Go（2026-10-23）

- [x] 壳侧 `shell.trashItem`（限定 libraryRoot、拒绝链接/目录）；Go 校验任务空闲与 confirmed、`ManagedFile` 边界、回收站调用、DB 事务清理（course_files/practice_sets/lecture ppt/transcript）并 checkpoint。
- [x] 测试：未确认拒绝、文件记录与课次 PPT 字段清理；capabilities 72 → **73**。

## 1.35 study:get/save/parse（2026-10-23）

- [x] `study_entries` 读写：UUID/课程/课次校验、删除重建事务、课次与聊天会话标题同步、`checkAt=endsAt+4h`。
- [x] `study:parse`：课程/课次上下文 + 课表提示词 + 结构化校验（最多 1000 条、编号去重、endsAt 时区）。
- [x] 测试：字段 trim/checkAt、课次/会话改名、重复/非法时间拒绝、AI 解析；capabilities 73 → **76**。

## 1.36 字幕保存/导入与拆题（2026-10-23）

- [x] `subtitles:save/import`：SRT 严格解析、字幕写库、校订检查点/笔记/练习清理、课次状态更新、详情回读；导入经 `shell.pickFile`。
- [x] `questions:split`：历年卷归属/OCR 校验、14k 分块、`past-exam` 提示词、逐块进度、题目字段白名单校验、题库替换与练习复位。
- [x] 测试：字幕清理/状态、拆题生成与题库落库、缺失历年卷拒绝；capabilities 76 → **79**。

## 1.37 练习作答与重置（2026-10-23）

- [x] `practice:submit/reset`：选项解析、答案字母核对、attempt 写入/清除、practice_sets 持久化。
- [x] 测试：正确/错误判定、重置、缺题拒绝；capabilities 79 → **81**。

## 1.38 notes:save（2026-10-23）

- [x] 笔记结构校验、原文/时间邻近核验（关键词触发、不确定段排除、“学生”开头拒绝）、`lecture_notes` 保存与练习复位、Notes Markdown 落盘（等级/核验标记/课件引用）。
- [x] 测试：核验真/假、Markdown 内容、练习清理；capabilities 81 → **82**。

## 1.39 M3 语义向量（Rust ort + tokenizers，2026-10-24）

- [x] 模型获取：`scripts/model-fetch.mts` 使用 undici `EnvHttpProxyAgent`（尊重 HTTP(S)_PROXY/NO_PROXY、关闭 H2）并对 429/5xx 重试；ASR/语义模型获取统一入口，新增 `acquire:asr`/`acquire:embedding` 脚本。
- [x] Rust `--mode embed`：`ort` 2.0 + `tokenizers`，加载 `onnx/model_quantized.onnx`，320/400 字窗口、查询前缀、CLS 池化、L2 归一化；`embed.text`/`embed.batch`。
- [x] `pnpm parity:embedding`：与 TS `KnowledgeEmbedding` 对比 3 组文本，余弦 ≥0.99（q8 量化与 fp32 本身约 0.9926；不同 ORT 版本量化内核约 1e-2 差异）。
- [x] Go：`helper` 新增 embed 助手与 `--embed-model`；知识库 `vector-status/enable/rebuild`（inventory digest、批量嵌入、事务发布、索引过期回退）；`knowledge:search` 在索引就绪时走语义+关键词 RRF，否则关键词。
- [x] 测试：向量状态/启用/发布/过期回退/语义检索、app 级状态与守卫、真实模型 enable→rebuild→search 集成；capabilities 82 → **85**。
- [ ] ASR：docling-asr 适配当前 ONNX 导出的转写结果异常（"the" 重复/英文空段），依赖已移除；待改用 whisper.cpp/Rust 自研或修正导出后继续。

## 1.40 M3 ASR 引擎（2026-10-24）

- [x] 定位 ASR 失败原因：q8 **encoder** 与 docling 解码不兼容（fp16 encoder + q8 decoder 与 TS 输出一致：`機器學習課堂筆記,重點是特徵工程和數據清洗`，时间 0–5.12s）。
- [x] 模型获取扩展：`encoder_model_fp16.onnx`（Go 路径）+ `decoder_model_quantized.onnx`（Go 路径），保留 q8 encoder/merged decoder 供 TS 回退；清单 16 文件、587MB。
- [x] Rust `--mode asr`：docling-asr Whisper 解码（fp16 encoder + q8 decoder）、语言 zh/auto、f32le PCM、时长/片段输出；`asr.transcribe` 实测通过。
- [x] Go `helper` 新增 ASR 助手与 `--asr-model`（resources/asr/onnx-small）；依赖说明（docling-asr MIT、whisper-small Apache-2.0）。
- [x] 已接入 `subtitles:generate`（VAD 分段 ASR + LLM 校订 + PPT 上下文工作流，见 1.43）。

## 1.41 notes:generate（2026-10-24）

- [x] 字幕上下文（`[HH:MM:SS]`）分块 + 课件缓存解析（来源身份/限制/扫描 PDF/12k 截断）+ `notes` 提示词 + 结构化校验；逐块进度、按 quote+timestamp 去重、原文核验、笔记落库与 Markdown。
- [x] `courseware.ParseCacheWithIdentity` 对齐 TS 的 stale/verified/unknown 判定。
- [x] 测试：生成/核验/落文件、缺失课次；capabilities 85 → **86**。

## 1.42 打包二进制链路（2026-10-24）

- [x] `scripts/build-binaries.mts`：统一构建 Go core（trimpath/strip）与 Rust release，并复制到 `backend/bin/zynote-core(.exe)`、`zynote-native(.exe)`；`backend:build`、`dev` 引导、`release` 均复用。
- [x] extraResources 与 `nativeBinaryPath/coreBinaryPath` 支持 Windows `.exe`。
- [x] 验证：release 构建产出 core 12MB + native 25MB（含 ASR/embeddings）。

## 1.43 subtitles:generate（2026-10-24）

- [x] Rust `--mode asr`：Silero V6 VAD 推理（512 帧、64 上下文、h/c 状态批处理）、概率阈值/静音合并/区间填充；逐语音区间转写并累加时间戳；区间合并单测 + 真实语音冒烟（0–5.12s）。
- [x] Go `internal/asr`：ffmpeg 16k mono f32le 提取、`asr-tasks/<uuid>/recovery.json`（decoding/transcribing/ready-to-save）、原生 `asr.transcribe` 上下文取消、cue 校验/排序/重叠裁切/SRT；fake ffmpeg 与取消/非法 cue 单测。
- [x] Go 校订工作流 `subtitles:generate`：原文与课件读取、课件缓存保留/重写、与 TS 字节一致的 fingerprint、输出预算分批、按相关性择取课件文字与图片、最多 2 批并发且按原文顺序提交、连接重试/冷却/Retry-After、检查点与状态、进度/取消、`courses/<course>/transcript-results/<lecture>/校订字幕.srt`、详情回读。
- [x] 对齐：PPTX 页面文本拼接与提取限制对齐 TS；`correctionContext` 与 fingerprint 加入 TS golden 对比；app 级 fake LLM 覆盖文本/视觉图片/断点续订/缺原文。
- [x] capabilities 86 → **87**；测试 `app` 新增字幕生成 3 例、`learning` 11 例、`asr` 5 例、Rust VAD 5 例。
- [ ] 路由仍默认 TS（与 notes:generate 一致）；`video-ppt:subtitle-transcribe-local` 尚未接 Go ASR 管线，留待视频域迁移。

## 1.44 practice:answer/prepare/generate（2026-10-24）

- [x] 共享练习逻辑：选项/答案字母解析、作答对比、关键词候选、Tavily 与 Bing RSS 搜索（20s/45s 超时、来源过滤、正文 15k 截断）。
- [x] `PrepareAnswer`：题库答案优先、同题搜索来源逐字核验（引文 ≥20 字、题干/答案同时命中）、AI 兜底、`correctOptionKeys`；`practice:answer`/`practice:prepare` 按题目/全量准备并落库。
- [x] `practice:generate`：缺笔记时先复用 `generateNotes`；历年题按 30 条分批匹配、5 道自测题、Tavily 拓展来源核验（题干/答案必须出现在来源中）、答案准备、`shortage` 与 `practice_sets` 落库。
- [x] 测试：`learning` 练习工具/PrepareAnswer/搜索 9 例；app 级 fake LLM + fake Tavily 覆盖 answer、generate、拓展题与持久化。
- [x] capabilities 87 → **90**。
- [ ] 路由仍默认 TS（与 subtitles/notes 一致）；`practice:generate` 的联网拓展需在切路由前做真实 Tavily 手测。

## 1.45 settings:save（2026-10-24）

- [x] 校验：视频限制、配置名称/Base URL、HTTP(S) 与凭据、查询参数/片段、temperature/topP/maxTokens 范围；换址且未填新 Key 时拒绝复用旧密钥。
- [x] 加密与合并：经壳侧 `crypto.protect`（safeStorage）写入 API Key/Tavily Key；保留未知旧字段、prompts/provider/theme/宽度/限制，原子替换 `settings.json`；清空搜索密钥时删除字段。
- [x] 守卫与重连：任务运行中且模型配置变化时拒绝保存；连接指纹变化后经 `tools.configure` 重配研究工具，未启动时跳过。
- [x] 壳侧新增 `crypto.protect` 与 `tools.configure` 反向服务；Go 端新增 `config.SaveSettings`。
- [x] 测试：config 5 例（加密合并/保留凭据/换址拒绝/忙碌守卫/校验）、app 级 settings:save 1 例；capabilities 90 → **91**。
- [ ] 路由仍默认 TS；切 Go 主路径前补安全的真实 safeStorage 冒烟。

## 1.46 textbook 文件生命周期（2026-10-24）

- [x] `textbook:pick/bytes/open/manual/clear/apply`：PDF 选择与页数读取、SHA256 指纹、随机令牌 HTTP Range 预览、系统打开、手动划线、清空本软件划线、草稿按优先级合并后写回。
- [x] Go `internal/textbook`：选区/草稿内存状态、`pdf.inspect`/`pdf.write` 原生调用、`prioritizedHighlights` 重叠裁切、临时文件/`.划线前备份.pdf`/原子替换/保存后复核；壳侧新增 `shell.openPath`。
- [x] 测试：优先级裁切、选择/预览 Range/手动划线/清空/草稿应用、备份与写回；capabilities 91 → **97**。
- [ ] `textbook:analyze/analyze-images/ppt-source/ppt-ocr` 仍走 TS；这些 action 与已迁移的选择状态耦合，切路由前必须整组 10 条一起切。
- [ ] `textbook:pick` 的「课本」默认目录搜索尚未迁移（当前使用系统选择器默认目录）。

## 1.47 textbook AI 划线（2026-10-24）

- [x] `textbook:analyze`：Rust `pdf.text` 逐行坐标 + 四类来源（课堂/教师重点/PPT/学长笔记）+ 每页 AI 决策、逐字引文/来源核验、坐标换算、草稿合并。
- [x] `textbook:analyze-images`：扫描页 JPEG 预览 → 视觉模型归一化坐标 → 页尺寸/旋转校验 → 草稿。
- [x] `textbook:ppt-source`/`ppt-ocr`：课件缓存探测、扫描 PDF 取回、哈希复核、视觉 OCR 回写 `ppt_text`（ocr 来源 + 限制说明）。
- [x] capabilities 97 → **101**；全部 textbook 10 条已具备 Go 实现，切路由可整组切换。
