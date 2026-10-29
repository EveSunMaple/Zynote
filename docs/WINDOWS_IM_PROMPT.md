# Windows IM 可复制提示词

把下面整段文字复制给 Windows 实机上的编码代理。

```text
你负责在 Windows 10/11 x64 实机上继续完成 Zynote 的 Windows IM 迁移。仓库是 EveSunMaple/Zynote，默认分支 main。开始前先阅读仓库根 AGENTS.md 和 docs/WINDOWS_IM_HANDOFF.md，并检查当前 git 状态和最新提交。

背景：P7 已完成，Go 是唯一后端，TypeScript IM 回退已删除；Windows 的 weixin_local 和 NapCat 内置后台仍标记不可用，是唯一剩余功能缺口。不要在未完成实机验证时把能力标记为可用。

任务：按 handoff 的 W1-W5 顺序实现：
1. Rust --mode wechat：进程枚举、账号目录识别、内存密钥扫描、SQLCipher 只读、session 兼容、预算和路径/文件身份校验。
2. Go WeixinLocal：availability、personal-processes/probe/confirm、sync/directory；send 明确拒绝；旧 im-connections.sqlite3 和 session 兼容。
3. NapCat 托管：zip SHA-256 校验、安全解包、owner 凭据、端口、WebUI 配置、node.exe、Job Object、登录/重启/退出清理。
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
