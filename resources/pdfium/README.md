# PDFium 动态库

本目录存放 Zynote 本地 PDF 文本/批注使用的 PDFium 动态库（`libpdfium.dylib`、`pdfium.dll` 或 `libpdfium.so`）。二进制体积较大，不纳入版本库；打包时由 `extraResources` 复制到应用资源目录 `pdfium/`。

开发环境获取：从 https://pdfium.googlesource.com/pdfium/ 或你信任的构建渠道取得与 macOS/Windows/Linux 架构匹配的共享库，放入本目录，命名为：

- macOS：`libpdfium.dylib`
- Windows：`pdfium.dll`
- Linux：`libpdfium.so`

许可见 `docs/legal/THIRD_PARTY_NOTICES.md` 的 PDFium 条目（BSD 3-Clause）。
