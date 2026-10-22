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

- [ ] PCM 提取、Silero VAD、Whisper 分片、时间戳合并。
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

## 16. 待确认问题

1. Electron 壳是否长期保留，还是未来还要迁 Tauri？如果未来迁 Tauri，contract 和 Go core 可以复用，但 shell 反向 RPC 需要重做。
2. 产品名是否从“摘云/梅酱”改为 Zynote？`appId` 和 `userData` 是否保持不变？
3. 是否接受 YouTube/PDF/SnapAny/IM 以 Node helper 形式长期存在？
4. 第一版是否必须三平台同时发布，还是 Windows x64 优先？
5. 是否保留 Windows 专属的微信只读与内置 QQ 后台？
6. 数据迁移是自动执行，还是首次启动让用户确认备份后再执行？
7. 是否继续遵守 AGPL-3.0-only，还是需要重新评估许可证？
8. 旧仓库是否继续维护 0.1.x 修复，还是冻结后只做安全修复？
