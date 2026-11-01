// 获取 PDF 文本读取所需的 PDFium 动态库，放到 resources/pdfium/（打包时由 extraResources 带入安装包）。
// 版本固定为 chromium/7543：与 native 依赖的 pdfium-render 0.8.37 默认绑定（pdfium_latest = pdfium_7543）一致，
// 版本不符时 zynote-native 绑定会失败（GetProcAddress）。来源 bblanchon/pdfium-binaries，按 SHA256 校验后才解压。
// 用法：node scripts/fetch-pdfium.mts [--force]；已有动态库时默认保留不覆盖。
import {createHash} from 'node:crypto'
import {spawnSync} from 'node:child_process'
import {existsSync} from 'node:fs'
import {copyFile, mkdir, mkdtemp, rm, writeFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'
import {modelFetch} from './model-fetch.mts'

const release = 'chromium/7543'
const builds: Record<string, {asset: string; member: string; name: string; sha256: string}> = {
 'win32-x64': {
  asset: 'pdfium-win-x64.tgz',
  member: 'bin/pdfium.dll',
  name: 'pdfium.dll',
  sha256: '0b08b606792a6cc593426efdefc6622611bce446d9e0270743846956ea1554ca'
 },
 'darwin-arm64': {
  asset: 'pdfium-mac-arm64.tgz',
  member: 'lib/libpdfium.dylib',
  name: 'libpdfium.dylib',
  sha256: '41c269723b4711793de70ff34e65c00fa79907d6c023741837579e906b846faa'
 },
 'linux-x64': {
  asset: 'pdfium-linux-x64.tgz',
  member: 'lib/libpdfium.so',
  name: 'libpdfium.so',
  sha256: '9329a3c4b19b3c8d0a93af5440f44be84e4bd879a204e47b1a7a160e96809da4'
 }
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const build = builds[`${process.platform}-${process.arch}`]
if (!build) {
 console.warn(`PDFium: ${process.platform}-${process.arch} 没有预置版本，跳过；该平台的 PDF 文本读取不可用`)
 process.exit(0)
}
const target = join(root, 'resources', 'pdfium', build.name)
if (existsSync(target) && !process.argv.includes('--force')) {
 console.log(`PDFium: 已有 ${target}，保留不覆盖（需要换成 ${release} 时加 --force）`)
 process.exit(0)
}
const url = `https://github.com/bblanchon/pdfium-binaries/releases/download/${encodeURIComponent(release)}/${build.asset}`
const response = await modelFetch(url)
if (!response.ok) throw new Error(`PDFium 下载失败：HTTP ${response.status}`)
const archive = Buffer.from(await response.arrayBuffer())
const digest = createHash('sha256').update(archive).digest('hex')
if (digest !== build.sha256) throw new Error(`PDFium 校验失败：期望 ${build.sha256}，实际 ${digest}`)
const work = await mkdtemp(join(tmpdir(), 'zynote-pdfium-'))
try {
 await writeFile(join(work, build.asset), archive)
 // 在临时目录内用相对路径解压：GNU tar 会把 Windows 绝对路径里的 "C:" 当成远程主机。
 const extracted = spawnSync('tar', ['-xzf', build.asset, build.member], {cwd: work, stdio: 'inherit'}) // NOSONAR: build script runs the runner's trusted tar on a checksum-verified archive.
 if (extracted.status !== 0) throw new Error('PDFium 解压失败')
 await mkdir(dirname(target), {recursive: true})
 await copyFile(join(work, build.member), target)
} finally {
 await rm(work, {recursive: true, force: true})
}
console.log(`PDFium: 已放置 ${release} → ${target}`)
