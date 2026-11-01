# PDFium 动态库

本目录存放 Zynote 本地 PDF 文本/批注使用的 PDFium 动态库（`libpdfium.dylib`、`pdfium.dll` 或 `libpdfium.so`）。二进制体积较大，不纳入版本库；打包时由 `extraResources` 复制到应用资源目录 `pdfium/`。

开发环境获取：运行 `pnpm pdfium:fetch`，按当前平台下载 bblanchon/pdfium-binaries 的 chromium/7543 版并校验 SHA256（`pnpm release` 也会自动执行；已有文件时保留，加 `--force` 覆盖）。版本须与 native 依赖的 pdfium-render 默认绑定一致，否则绑定失败。也可手动从 https://pdfium.googlesource.com/pdfium/ 或你信任的构建渠道取得与 macOS/Windows/Linux 架构匹配的共享库，放入本目录，命名为：

- macOS：`libpdfium.dylib`
- Windows：`pdfium.dll`
- Linux：`libpdfium.so`

许可见 `docs/legal/THIRD_PARTY_NOTICES.md` 的 PDFium 条目（BSD 3-Clause）。
