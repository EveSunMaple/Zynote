# AGENTS.md — Zynote

本文件是仓库级开发约束，适用于所有人和所有 AI 代理。

## 1. 项目速览

- Electron + React 19 桌面应用；后端迁移至 Go，原生硬模块迁移至 Rust。
- 三语言栈：TS（Electron 壳 + preload + 渲染层）、Go（`backend/`）、Rust（`native/`）。
- 上游基线：`meiji-listening-guide` 0.1.85；`src/main` 中的 TS 业务后端为过渡期回退实现，P7 删除。
- 检查入口：`pnpm verify`（TS）、`pnpm backend:test`（Go）、`pnpm native:test`（Rust）。

## 2. 已锁定决策

- macOS 优先（arm64）；Windows/Linux 功能按能力矩阵对齐后另行发布。
- `appId`（`org.listening-assistant.desktop`）与 `userData` 目录沿用，保证老用户数据可迁移。
- 产品展示名 Zynote，首版 0.0.1。
- 全功能对齐（B1）：每个域的黄金样本对比通过才能切默认后端。
- 数据目录英文化：`Library/`、`Data/`、`Transcripts/`、`Notes/`、`PastExams/`、`Knowledge/`、`MediaDownloads/`、`SubtitleSync/`、`NoteGeneration/`、`VisualOutputs/`、`Answers/`；首次启动自动迁移旧中文目录。
- 零 meijiang：新代码不得出现 meijiang/meiji 字样（环境变量 `ZYNOTE_*`、临时目录 `zynote-*`）。仅两处冻结例外（数据格式兼容，不可改）：
  1. `src/main/documents/pdf-annotations.ts` 的 `meijiang:`/`MeijiangData` 前缀——回退后端必须与 0.1.85 字节兼容；Rust PDF 助手按 C2 双前缀读取、写 `zynote:`。
  2. `NOTICE` 与 `docs/legal/` 中对上游仓库的归属 URL——AGPL 归属要求。

## 3. 目录结构

```text
backend/      Go module（cmd/zynote-core + internal/*）
native/       Rust workspace（zynote-native，单二进制多 --mode）
src/main/     Electron 壳 + 过渡期 TS 回退后端
src/preload/  contextBridge 桥
src/renderer/ React UI（features/<域>）
src/shared/   类型、协议、纯函数（TS 侧）
contract/     JSON Schema、action 清单、fixtures（跨语言契约源）
resources/    图标、prompts、可选运行组件（大文件不提交）
scripts/      构建、冒烟、发布
docs/         正式文档；docs/tmp/ 放临时草稿（gitignored）
```

## 4. 规则

- 跨目录 import 用别名 `@shared/*`、`@main/*`、`@renderer/*`；禁止 `../../`。Go/Rust 用显式包路径，禁止 barrel。
- Go：`context.Context` 第一参数；错误 `%w` 包装并映射稳定错误码；`log/slog` 结构化日志，正文/Cookie/密钥/联系人不入日志；`errgroup` + 有界并发；可能 native crash 的模块放独立进程。
- 协议：JSON-RPC 2.0 over stdio；stdout 只走协议，日志/panic 走 stderr；大文件走路径不进 JSON；帧上限 8 MiB。
- Electron 壳不承载业务规则；Go 不 import Electron；双向通过 `contract/` 契约对齐。
- 渲染层样式沿用上游规范：颜色/圆角/阴影只用 `styles/upstream-tokens.css` 令牌；唯一主色 `--blue`；HeroUI 组件优先；禁止内联样式、`!important`、裸标签选择器；按钮层级与图标规则同上游 AGENTS。
- 提交前：`pnpm verify`、`pnpm backend:test`、`pnpm native:test` 全绿；涉及界面改两套主题目检；涉及打包改 `pnpm build` 后检查 `out/` 与 `backend/bin/` 产物。

## 5. 迁移状态

阶段与门禁见 `docs/MIGRATION_CHECKLIST.md`（A4/B1/C2/D 组定稿版）。
