import {spawn} from 'node:child_process'
import {mkdtemp, readFile, rm, writeFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join, dirname, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'
import {PDFDocument, StandardFonts, PDFHexString, PDFName, PDFNumber, PDFArray, rgb} from 'pdf-lib'

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const work = await mkdtemp(join(tmpdir(), 'znr-'))
const basePdf = join(work, 'base.pdf')
const outputPdf = join(work, 'output.pdf')

const hex = (value: string) => PDFHexString.fromText(value)

const pdfDoc = await PDFDocument.create()
const font = await pdfDoc.embedFont(StandardFonts.Helvetica)
const page = pdfDoc.addPage([400, 300])
page.drawText('annotation roundtrip', {x: 40, y: 200, size: 14, font, color: rgb(0, 0, 0)})
const legacyAnnotation = pdfDoc.context.obj({
 Type: 'Annot',
 Subtype: 'Highlight',
 Rect: pdfDoc.context.obj([10, 240, 210, 260]).asArray(),
 QuadPoints: pdfDoc.context.obj([10, 260, 210, 260, 10, 240, 210, 240]).asArray(),
 C: pdfDoc.context.obj([0.76, 0.61, 0.44]).asArray(),
 CA: pdfDoc.context.obj(0.3),
 F: pdfDoc.context.obj(4),
 NM: PDFHexString.fromText('meijiang:legacy-1'),
 T: PDFHexString.fromText('摘云'),
 Contents: PDFHexString.fromText('旧批注'),
 MeijiangData: PDFHexString.fromText(
  JSON.stringify({id: 'legacy-1', source: 'teacher', quote: '旧批注内容', evidence: '依据', extension: false})
 )
})
const legacyRef = pdfDoc.context.register(legacyAnnotation)
page.node.set(PDFName.of('Annots'), pdfDoc.context.obj([legacyRef]))
await writeFile(basePdf, await pdfDoc.save())

const run = (request: string) =>
 new Promise<{result?: any; error?: {message: string}}>((resolve, reject) => {
  const child = spawn(
   join(projectRoot, 'native', 'target', 'release', 'zynote-native'),
   ['--mode', 'pdf', '--pdfium', join(projectRoot, 'resources', 'pdfium', 'libpdfium.dylib')],
   {stdio: ['pipe', 'pipe', 'pipe']}
  )
  const timer = setTimeout(() => {
   child.kill()
   reject(new Error('rust 超时'))
  }, 15000)
  timer.unref?.()
  let buffer = ''
  child.stdout.on('data', chunk => {
   buffer += String(chunk)
   const index = buffer.indexOf('\n')
   if (index < 0) return
   const frame = JSON.parse(buffer.slice(0, index))
   buffer = ''
   if (frame.result?.mode) return
   clearTimeout(timer)
   child.kill()
   frame.error ? reject(new Error(frame.error.message)) : resolve(frame)
  })
  child.stderr.on('data', chunk => console.error('[rust-stderr]', String(chunk).trim()))
  child.stdin.write(request + '\n')
 })

const inspected = await run(
 JSON.stringify({jsonrpc: '2.0', id: '1', method: 'pdf.inspect', params: {path: basePdf}})
)
const previous = inspected.result?.previous ?? []
if (previous.length !== 1 || previous[0].id !== 'legacy-1' || previous[0].page !== 1) {
 console.error('FAIL inspect previous:', JSON.stringify(previous))
 process.exit(1)
}
console.log('ok inspect legacy (meijiang 前缀可读)')

const rect = previous[0].rect as number[]
const writeReply = await run(
 JSON.stringify({
  jsonrpc: '2.0',
  id: '2',
  method: 'pdf.write',
  params: {
   path: basePdf,
   output: outputPdf,
   rows: [
    previous[0],
    {
     id: '11111111-1111-4111-8111-111111111111',
     page: 1,
     rect: [rect[0], rect[1] - 25, rect[2], 20],
     source: 'ppt',
     quote: '新批注内容',
     evidence: '新依据',
     extension: true,
     color: [0.53, 0.76, 1],
     description: 'Zynote 批注'
    }
   ]
  }
 })
)
if (!writeReply.result || writeReply.result.count !== 1 || writeReply.result.pages !== 1) {
 console.error('FAIL write:', JSON.stringify(writeReply))
 process.exit(1)
}
console.log('ok write (count=1 pages=1)')

const reopened = await PDFDocument.load(await readFile(outputPdf), {updateMetadata: false})
const annots = reopened.getPage(0).node.lookupMaybe(PDFName.of('Annots'), PDFArray)
const decoded = (annots?.asArray() ?? []).map(ref => {
 const dict = reopened.context.lookup(ref)
 const get = (key: string) => {
  const value = dict.get(PDFName.of(key))
  return value instanceof PDFHexString ? value.decodeText() : ''
 }
 return {nm: get('NM'), data: get('ZynoteData') || get('MeijiangData'), title: get('T')}
})
const legacyMigrated = decoded.some(
 row =>
  row.nm === 'zynote:legacy-1' &&
  row.data.includes('legacy-1') &&
  row.data.includes('旧批注内容') &&
  row.title === 'Zynote'
)
const hasCurrent = decoded.some(
 row =>
  row.nm.startsWith('zynote:') &&
  row.data.includes('11111111-1111-4111-8111-111111111111') &&
  row.data.includes('新批注内容') &&
  row.title === 'Zynote'
)
if (!legacyMigrated || !hasCurrent) {
 console.error('FAIL roundtrip annotations:', JSON.stringify(decoded))
 process.exit(1)
}
console.log('ok roundtrip（旧 meijiang 批注迁移为 zynote + 新批注写入）')

const reInspect = await run(
 JSON.stringify({jsonrpc: '2.0', id: '3', method: 'pdf.inspect', params: {path: outputPdf}})
)
const afterRows = reInspect.result?.previous ?? []
if (afterRows.length !== 2) {
 console.error('FAIL reinspect:', JSON.stringify(afterRows))
 process.exit(1)
}
console.log('ok reinspect（C2 双前缀读取到两批批注）')

await rm(work, {recursive: true, force: true})
process.exit(0)
