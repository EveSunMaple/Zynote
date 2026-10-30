# 发布与自动更新

## 1. 版本与清单

1. 修改 `package.json` 的 `version`。
2. 同步 `resources/runtime-manifest.json` 的 `productVersion`；组件清单非空时，完整包还必须包含 `resources/video` 等已审阅组件。
3. 运行 `pnpm verify`、`pnpm backend:test`、`pnpm native:test`、`pnpm smoke:go-core`。

## 2. 构建

```sh
pnpm build
pnpm release lite --platform=mac --prepared
pnpm release lite --platform=win --prepared
pnpm release lite --platform=linux --prepared
```

产物：

- lite：安装包 + `latest*.yml` + blockmap；自动更新走 `latest` 通道。
- full：仅 Windows；组件必须打包；自动更新走 `full` 通道。
- portable：目录版，不生成更新元数据，不检查更新。
- components：同版本组件包；Runtime v2 安装使用。

`scripts/release.mts` 会校验更新元数据版本、安装包路径，以及 portable 不含更新元数据。

Windows 的 lite/full 包在 `resources/knowledge/vendor/NapCat.Shell.Windows.Node.zip` 存在时会把它放入 `resources/knowledge/vendor/`。固定版本为 NapCat `4.18.37`，SHA-256 为 `05c79761361321f6af4358aef92f80a7f8e319b6459c260ebf9a159615e96fe4`；缺失时该资源会被跳过，内置 QQ 能力在运行时显示不可用。

## 3. 签名与公证

GitHub Actions 的 `release.yml` 使用以下 secrets：

- macOS：`MAC_CSC_LINK`、`MAC_CSC_KEY_PASSWORD`、`APPLE_ID`、`APPLE_APP_SPECIFIC_PASSWORD`、`APPLE_TEAM_ID`
- Windows：`WIN_CSC_LINK`、`WIN_CSC_KEY_PASSWORD`

手动触发 `release.yml` 时：

- `profile=lite/portable` 构建对应安装包。
- `publish=true` 要求签名 secrets，然后创建/更新 `v<version>` GitHub Release，上传安装包、`latest*.yml`/`full*.yml`、blockmap、checksums。
- 未签名产物不得作为正式自动更新源。

## 4. 自动更新验收

```sh
pnpm package:smoke --app release/lite/mac-arm64/Zynote.app/Contents/MacOS/Zynote
pnpm e2e:packaged --app release/lite/mac-arm64/Zynote.app/Contents/MacOS/Zynote
pnpm update:smoke --app release/lite/mac-arm64/Zynote.app/Contents/MacOS/Zynote
```

发布后必须再做一次真实升级：旧版启动 → 检查到新版本 → 下载 → 重启安装 → 新版本启动并读取旧数据。

## 5. 数据与回滚

```sh
pnpm migration:drill --app <packaged app>
```

该演练验证：旧库备份、`user_version` 升级、资料路径重定位、备份库仍保留旧路径。正式发布前再用真实旧版本数据副本做一次“新版本迁移 → 恢复旧安装包/旧库 → 再读取”。

## 6. 外部阻塞

- `resources/video` 的 FFmpeg/FFprobe 需要固定版本、许可证、SHA-256、签名/公证。
- 非空 `resources/runtime-manifest.json` 才能构建完整包。
- Developer ID/公证证书、Windows 代码签名证书必须由发布者提供。
