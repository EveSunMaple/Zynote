import {_electron, type ElectronApplication, type Page} from 'playwright'
import {copyFileSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync} from 'node:fs'
import {createServer} from 'node:http'
import {once} from 'node:events'
import {tmpdir} from 'node:os'
import {join, resolve} from 'node:path'

type AssistantWindow = typeof window & {assistant: {invoke: (action: string, args?: unknown) => Promise<unknown>}}

const appIndex = process.argv.indexOf('--app')
if (appIndex < 0 || !process.argv[appIndex + 1])
 throw new Error('Usage: node scripts/packaged-e2e.mts --app <packaged executable>')
const executablePath = resolve(process.argv[appIndex + 1])
const dataRoot = mkdtempSync(join(tmpdir(), 'zynote-e2e-'))
let application: ElectronApplication | undefined
function check(value: unknown, message: string): asserts value {
 if (!value) throw new Error(message)
}

async function checkTextbookCsp(page: Page) {
 let hits = 0
 const server = createServer((_request, response) => {
  hits++
  response.end('unrelated service')
 })
 try {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  const address = server.address()
  check(address && typeof address === 'object', 'missing probe port')
  const blocked = await page.evaluate(async url => {
   const timeout = AbortSignal.timeout(5000)
   const reported = new Promise<boolean>(resolve => {
    timeout.addEventListener('abort', () => resolve(false), {once: true})
    document.addEventListener(
     'securitypolicyviolation',
     event => {
      if (
       event.effectiveDirective === 'connect-src' &&
       event.disposition === 'enforce' &&
       new URL(event.blockedURI, document.baseURI).origin === new URL(url).origin
      )
       resolve(true)
     },
     {signal: timeout}
    )
   })
   const reached = await fetch(url, {mode: 'no-cors', signal: timeout}).then(
    () => true,
    () => false
   )
   return !reached && (await reported)
  }, `http://127.0.0.1:${address.port}/textbooks/probe`)
  check(blocked && hits === 0, 'connect-src must block unrelated loopback textbook endpoints')
 } finally {
  server.close()
 }
}

function enlargePdfFixture(path: string) {
 const pdf = readFileSync(path)
 // Keep all object offsets valid and move only the xref table to a later chunk.
 const xref = pdf.indexOf('\nxref\n') + 1
 check(xref > 0, 'missing fixture xref table')
 const padding = Buffer.alloc(3 * 1024 * 1024, 32)
 const tail = pdf
  .subarray(xref)
  .toString('ascii')
  .replace('startxref\n' + xref, 'startxref\n' + (xref + padding.length))
 writeFileSync(path, Buffer.concat([pdf.subarray(0, xref), padding, Buffer.from(tail)]))
}

try {
 application = await _electron.launch({
  executablePath,
  args: process.platform === 'linux' ? ['--no-sandbox'] : [],
  env: {...process.env, LISTENING_ASSISTANT_DATA: dataRoot}
 })
 const page = await application.firstWindow({timeout: 45000})
 await page.waitForLoadState('domcontentloaded')
 await checkTextbookCsp(page)
 const invoke = <T,>(action: string, args?: unknown) =>
  page.evaluate(({action, args}) => (window as AssistantWindow).assistant.invoke(action, args), {
   action,
   args
  }) as Promise<T>
 const info = await invoke<{version: string; platform: string}>('app:info')
 check(info.version === '0.0.1' && info.platform === process.platform, 'app:info mismatch')
 const health = await invoke<{ok: boolean; integrity: string}>('db:health')
 check(health.ok && health.integrity === 'ok', 'db:health mismatch')
 // Legacy clients could send a whole set of page images and break stdin before Go could reject it.
 const oversized = await page.evaluate(() =>
  (window as AssistantWindow).assistant
   .invoke('textbook:analyze-images', {images: ['A'.repeat(8 * 1024 * 1024)]})
   .then(() => '', String)
 )
 check(oversized.includes('8 MiB'), 'oversized request should be rejected by the shell')
 check((await invoke<{ok: boolean}>('db:health')).ok, 'oversized request interrupted the backend')
 check((await invoke<unknown[]>('courses')).length === 0, 'fresh database should have no courses')
 const course = await invoke<{id: string; name: string}>('course:create', {name: 'E2E 课程'})
 check(course.name === 'E2E 课程' && course.id.length === 36, 'course:create mismatch')
 check((await invoke<unknown[]>('sessions')).length >= 1, 'default session missing')
 check(
  (await invoke<{course: {id: string}}>('course:detail', {id: course.id})).course.id === course.id,
  'course:detail mismatch'
 )
 await invoke('settings:get')
 await invoke('appearance:get')
 await invoke('identity:get')
 const runtime = await invoke<{components: unknown[]}>('runtime:status')
 check(Array.isArray(runtime.components), 'runtime:status mismatch')
 const im = await invoke<{platforms: {weixin_local: {available: boolean}; qq_napcat: {available: boolean}}}>(
  'tools:request',
  {action: 'im', args: {action: 'status', value: {}}}
 )
 check(im.platforms.weixin_local.available === (process.platform === 'win32'), 'weixin_local capability mismatch')
 function pdfiumLibraryName() {
  if (process.platform === 'win32') return 'pdfium.dll'
  if (process.platform === 'darwin') return 'libpdfium.dylib'
  return 'libpdfium.so'
 }
 const target = await application.evaluate(() => ({arch: process.arch, resources: process.resourcesPath}))
 const qqArchive = statSync(join(target.resources, 'knowledge', 'vendor', 'NapCat.Shell.Windows.Node.zip'), {
  throwIfNoEntry: false
 })
 const qqAvailable =
  process.platform === 'win32' && target.arch === 'x64' && !!qqArchive?.isFile() && qqArchive.size > 0
 check(im.platforms.qq_napcat.available === qqAvailable, 'qq_napcat capability mismatch')
 const update = await invoke<{phase: string}>('update:status')
 check(typeof update.phase === 'string', 'update:status mismatch')
 check(await invoke<boolean>('course:delete', {id: course.id, confirmed: true}), 'course:delete failed')
 check((await invoke<unknown[]>('courses')).length === 0, 'course was not deleted')
 // A weekly timetable can be saved on a fresh profile with no selected local course.
 await page.reload()
 const openSchedule = page.getByRole('button', {name: '课表与教材', exact: true})
 await openSchedule.click()
 await page.getByRole('button', {name: '上传课表图片', exact: true}).first().waitFor()
 check(
  (await page.getByRole('button', {name: '识别文字课表', exact: true}).count()) > 0,
  'text import should remain available'
 )
 const pdfiumFile = pdfiumLibraryName()
 if (statSync(join(target.resources, 'pdfium', pdfiumFile), {throwIfNoEntry: false})?.isFile()) {
  // Page one is a red JPEG 2000 scan. Page two uses a predefined Chinese CMap to draw blue text.
  const fixture = join(dataRoot, 'preview.pdf')
  copyFileSync(resolve('scripts/fixtures/textbook-preview.pdf'), fixture)
  await application.evaluate(({dialog}, file) => {
   dialog.showOpenDialog = () => Promise.resolve({canceled: false, filePaths: [file]})
  }, fixture)
  const previewPrefix = 'textbook-pdf://preview/textbooks/'
  const textbookRequests: string[] = []
  page.on('request', request => {
   if (new URL(request.url()).pathname.startsWith('/textbooks/')) textbookRequests.push(request.url())
  })
  await page.getByRole('tab', {name: '教材划线', exact: true}).click()
  await page.getByRole('button', {name: '打开教材 PDF', exact: true}).click()
  const waitForPageColor = (channel: number) =>
   page.waitForFunction(channel => {
    const canvas = document.querySelector<HTMLCanvasElement>('.study-page__canvas')
    const context = canvas?.getContext('2d')
    if (!canvas || !context) return false
    const {data} = context.getImageData(0, 0, canvas.width, canvas.height)
    for (let i = 0; i < data.length; i += 4) {
     if (data[i + channel] > 200 && data[i + (2 - channel)] < 30 && data[i + 1] < 30 && data[i + 3] === 255) return true
    }
    return false
   }, channel)
  await waitForPageColor(0)
  await page.getByRole('button', {name: '下一页', exact: true}).click()
  await waitForPageColor(2)
  // Reopen the same path after changing the PDF; the last chunk now holds its xref table.
  enlargePdfFixture(fixture)
  await Promise.all([
   page.waitForResponse(
    response =>
     response.url().startsWith(previewPrefix) &&
     response.status() === 206 &&
     /^bytes [1-9]\d*-\d+\/\d+$/.test(response.headers()['content-range'] ?? '')
   ),
   page.getByRole('button', {name: '更换教材 PDF', exact: true}).click()
  ])
  await waitForPageColor(0)
  check(
   textbookRequests.length > 0 && textbookRequests.every(url => url.startsWith(previewPrefix)),
   'unexpected textbook origin'
  )
 }
 // The native file chooser can stay open longer than the standard 10s IPC deadline.
 await application.evaluate(({dialog}) => {
  dialog.showOpenDialog = async () => {
   await new Promise(resolve => setTimeout(resolve, 11000))
   return {canceled: true, filePaths: []}
  }
 })
 check((await invoke('textbook:pick')) === null, 'canceling a delayed textbook selection should return null')
 console.log(JSON.stringify({status: 'passed', goCore: true, update: update.phase}))
} finally {
 await application?.close().catch(() => {})
 rmSync(dataRoot, {recursive: true, force: true})
}
