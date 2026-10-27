import {spawn} from 'node:child_process'
import {mkdtemp, readFile, writeFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join, dirname, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'
import {PDFDocument, StandardFonts, rgb} from 'pdf-lib'
import {getDocument} from 'pdfjs-dist/legacy/build/pdf.mjs'

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const work = await mkdtemp(join(tmpdir(), 'zynote-pdf-'))
const pdfPath = join(work, 'fixture.pdf')

const layout = [
 {height: 400, lines: ['Hello PDF highlight', 'Second line here', 'Third line ends']},
 {height: 420, lines: ['Second page', 'with two lines']}
]

const pdfDoc = await PDFDocument.create()
const font = await pdfDoc.embedFont(StandardFonts.Helvetica)
for (const spec of layout) {
 const page = pdfDoc.addPage([500, spec.height])
 spec.lines.forEach((text, index) => {
  page.drawText(text, {x: 50, y: spec.height - 50 - index * 30, size: 16, font, color: rgb(0, 0, 0)})
 })
}
await writeFile(pdfPath, await pdfDoc.save())

const open = getDocument({data: new Uint8Array(await readFile(pdfPath)), useSystemFonts: true})
const loaded = await open.promise
const expectedPages: {index: number; items: {text: string; x: number; y: number; width: number; height: number}[]}[] =
 []
for (let number = 1; number <= loaded.numPages; number++) {
 // biome-ignore format: NOSONAR must stay on the await line
 const page = await loaded.getPage(number) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
 // biome-ignore format: NOSONAR must stay on the await line
 const content = await page.getTextContent() // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
 expectedPages.push({
  index: number,
  items: content.items.flatMap(item =>
   'str' in item
    ? [
       {
        text: item.str,
        x: item.transform[4],
        y: item.transform[5],
        width: item.width,
        height: Math.max(item.height, Math.abs(item.transform[3]))
       }
      ]
    : []
  )
 })
}
await open.destroy()

const rustPath = join(projectRoot, 'native', 'target', 'release', 'zynote-native')
const pdfiumPath = join(projectRoot, 'resources', 'pdfium', 'libpdfium.dylib')
const child = spawn(rustPath, ['--mode', 'pdf', '--pdfium', pdfiumPath], {stdio: ['pipe', 'pipe', 'pipe']})
type PdfItem = {text: string; x: number; y: number; width: number; height: number}
type PdfPageData = {index: number; items: PdfItem[]}
type RustReply = {
 id?: string
 result?: {numPages: number; pages: PdfPageData[]}
 error?: {message: string}
}
const reply = await new Promise<RustReply>((resolveReply, reject) => {
 const timer = setTimeout(() => reject(new Error('rust 回复超时')), 15000)
 timer.unref?.()
 let buffer = ''
 child.stdout.on('data', chunk => {
  buffer += String(chunk)
  for (let index = buffer.indexOf('\n'); index >= 0; index = buffer.indexOf('\n')) {
   const line = buffer.slice(0, index)
   buffer = buffer.slice(index + 1)
   const frame = JSON.parse(line) as RustReply
   if (frame.id === '2') {
    clearTimeout(timer)
    resolveReply(frame)
   }
  }
 })
 child.stderr.on('data', chunk => console.error('[rust-stderr]', String(chunk).trim()))
 child.on('exit', code => {
  clearTimeout(timer)
  reject(new Error('rust 提前退出 code=' + code))
 })
 child.stdin.write(JSON.stringify({jsonrpc: '2.0', id: '1', method: 'core.hello'}) + '\n')
 child.stdin.write(JSON.stringify({jsonrpc: '2.0', id: '2', method: 'pdf.text', params: {path: pdfPath}}) + '\n')
})
child.kill()

if (!reply.result) {
 console.error('rust pdf.text 失败:', JSON.stringify(reply.error))
 process.exit(1)
}
const actualPages: PdfPageData[] = reply.result.pages

let failures = 0
const check = (name: string, ok: boolean) => {
 if (ok)
  console.log('ok', name) // NOSONAR: conditional expression mirrors the existing API contract.
 else {
  console.error('FAIL', name)
  failures++
 }
}

const normalize = (value: string) => value.replace(/\s+/g, ' ').trim()

check('numPages', reply.result.numPages === layout.length)
for (const expected of expectedPages) {
 const actual = actualPages.find(page => page.index === expected.index)
 if (!actual) {
  console.error('FAIL 缺页', expected.index)
  failures++
  continue
 }
 const expectedText = expected.items.map(item => normalize(item.text)).join('\n')
 const actualText = actual.items.map(item => normalize(item.text)).join('\n')
 const same = expectedText === actualText
 check('文本一致 第' + expected.index + '页', same)
 if (!same) {
  console.error(' 期望:', JSON.stringify(expectedText))
  console.error(' 实际:', JSON.stringify(actualText))
 }
 if (expected.items.length === actual.items.length) {
  const averages = {dx: 0, dy: 0, dw: 0, dh: 0}
  for (const [index, item] of expected.items.entries()) {
   const other = actual.items[index]
   averages.dx += Math.abs(item.x - other.x)
   averages.dy += Math.abs(item.y - other.y)
   averages.dw += Math.abs(item.width - other.width)
   averages.dh += Math.abs(item.height - other.height)
  }
  const count = expected.items.length
  console.log(
   '坐标偏差 第' + expected.index + '页 x:',
   (averages.dx / count).toFixed(2),
   'y:',
   (averages.dy / count).toFixed(2),
   'width:',
   (averages.dw / count).toFixed(2),
   'height:',
   (averages.dh / count).toFixed(2)
  )
 } else {
  console.log('条目数不同 第' + expected.index + '页', expected.items.length, 'vs', actual.items.length)
 }
}

process.exit(failures ? 1 : 0)
