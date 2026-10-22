# Zynote

课堂学习桌面应用：智云课堂字幕、教师重点标注与课后练习。macOS 优先的 Go 后端重写版本，上游基线为 `meiji-listening-guide` 0.1.85（TypeScript/Electron）。

## 架构

| 层 | 技术 | 职责 |
| --- | --- | --- |
| 渲染层 | React 19 + HeroUI v3 + Tailwind 4 | 界面，不变 |
| Electron 壳 | TypeScript | 窗口、对话框、session、系统密钥库桥、自动更新 |
| 业务核心 | Go（`backend/`） | 数据库、课程、媒体、知识库、IM 编排、任务 |
| 原生助手 | Rust（`native/`） | PDF、ASR/向量、YouTube、微信本地读取 |

进程间协议：stdio 上的 JSON-RPC 2.0（换行分隔），stdout 只走协议。

## 开发

需要 Node.js 24+、pnpm、Go 1.25+、Rust（cargo）。

```sh
pnpm install
pnpm backend:build
pnpm dev
```

## 检查

```sh
pnpm verify        # tsc + biome
pnpm backend:test  # go test
pnpm native:test   # cargo test
pnpm smoke:go-core # Go core 冒烟（不启动 Electron）
pnpm build
```

## 许可

AGPL-3.0-only；第三方归属见 `NOTICE`、`docs/legal/THIRD_PARTY_NOTICES.md`。
