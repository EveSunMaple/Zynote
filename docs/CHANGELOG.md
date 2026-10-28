# 更新日志

## Unreleased

### Zynote 0.0.1（测试版）

- 以 `meiji-listening-guide` 0.1.85 为基线启动 Go 后端重写；产品展示名改为 Zynote。
- 新增 Go 业务核心（`backend/`）：stdio JSON-RPC、握手、`app:info`/`settings:get`/`appearance:get`/`identity:get` 只读接口、SQLite 健康检查与备份。
- 新增 Rust 原生助手骨架（`native/`，多模式）。
- Electron 壳新增后端监管（spawn/握手/崩溃重启）与按域 IPC 路由；其余动作继续走 TS 回退后端。
- 数据目录英文化映射；`MEIJI_*` 环境变量改为 `ZYNOTE_*`；系统加密信封写 `zynote-` 前缀并兼容读取旧 `meiji-` 信封。
- Runtime v2 组件安装：支持校验、原子发布与 macOS/Linux 目标；教材选择恢复 `课本` 默认目录。
- 数据迁移保护：`user_version` 版本门禁、启动完整性检查、旧库自动备份到 `Data/Backups`。
- 自动更新：接入 `electron-updater`，新增「应用更新」设置页、后台检查/下载/重启安装、GitHub Releases 更新通道与 release 发布工步。
- P7：删除 TypeScript 业务后端与回退开关，Go core 成为唯一后端；`src/main` 仅保留 Electron 壳与系统桥。清理 SQLite/ONNX/DOCX 等生产依赖，macOS portable 应用体积由约 893MiB 降至约 507MB。启动时自动迁移 `资料`→`Library`、portable `课程数据`→`Data` 并重定位数据库内文件路径。
