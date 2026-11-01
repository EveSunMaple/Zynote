import {_electron, type ElectronApplication} from 'playwright'
import {mkdtempSync, rmSync, statSync} from 'node:fs'
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
try {
 application = await _electron.launch({
  executablePath,
  args: process.platform === 'linux' ? ['--no-sandbox'] : [],
  env: {...process.env, LISTENING_ASSISTANT_DATA: dataRoot}
 })
 const page = await application.firstWindow({timeout: 45000})
 await page.waitForLoadState('domcontentloaded')
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
  (window as AssistantWindow).assistant.invoke('textbook:analyze-images', {images: ['A'.repeat(8 * 1024 * 1024)]}).then(
   () => '',
   error => String(error)
  )
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
 const upload = page.getByRole('button', {name: '上传课表图片', exact: true})
 const more = page.getByRole('button', {name: '更多操作', exact: true})
 const addWeekly = page.getByRole('button', {name: '添加每周课程', exact: true})
 const openMore = async () => {
  if ((await more.getAttribute('aria-expanded')) !== 'true') await more.click()
 }
 await upload.waitFor()
 await page.getByRole('button', {name: '查看示例', exact: true}).click()
 const examples = page.getByRole('dialog', {name: '课表截图示例', exact: true})
 for (const label of ['教务系统', '手机彩色', '手机单色']) {
  await examples.getByRole('tab', {name: label, exact: true}).click()
  await examples
   .getByRole('tabpanel', {name: label, exact: true})
   .locator('img')
   .evaluate((image: HTMLImageElement) => image.decode())
 }
 await page.keyboard.press('Escape')
 await examples.waitFor({state: 'hidden'})
 const guide = page.locator('.study-next-step:visible')
 check((await guide.innerText()).includes('上传课表图片'), 'empty schedule guide must name the import action')
 check(
  (await more.getAttribute('aria-expanded')) === 'false' && !(await addWeekly.isVisible()),
  'import alternatives must start collapsed'
 )
 await openMore()
 await addWeekly.click()
 check((await guide.innerText()).includes('查看待补项'), 'incomplete draft should point to the missing field')
 await page.getByRole('textbox', {name: '课程名称', exact: true}).fill('周课表测试')
 check((await guide.innerText()).includes('保存课表'), 'valid draft should point to save')
 await page.getByRole('button', {name: '保存课表', exact: true}).click()
 await page.getByText('课表已保存，共 1 个课次。', {exact: true}).waitFor()
 const timetable = await invoke<Array<{title: string; courseId: string}>>('study:get')
 check(
  timetable.length === 1 && timetable[0].title === '周课表测试' && timetable[0].courseId === '',
  'unlinked timetable save failed'
 )
 const clearSchedule = page.getByRole('button', {name: '清空课表', exact: true})
 const clearDialog = page.getByRole('dialog', {name: '清空全部课表？', exact: true})
 const confirmClear = clearDialog.getByRole('button', {name: '确认清空', exact: true})
 const saveEmpty = page.getByRole('button', {name: '保存空课表', exact: true})
 const title = page.getByRole('textbox', {name: '课程名称', exact: true})
 await page.getByPlaceholder('搜索课次、章节或课程').fill('不存在的课次')
 await page.getByText('没有匹配的课次。', {exact: true}).waitFor()
 await openMore()
 await clearSchedule.click()
 check(/\b1\b/.test(await clearDialog.innerText()), 'clear confirmation must count filtered-out entries')
 await clearDialog.getByRole('button', {name: '取消', exact: true}).click()
 check((await title.inputValue()) === timetable[0].title, 'canceling clear discarded the draft')
 await clearSchedule.click()
 await confirmClear.click()
 await saveEmpty.waitFor()
 check(!(await clearSchedule.isVisible()), 'empty drafts must hide the clear action')
 check((await invoke<unknown[]>('study:get')).length === 1, 'clearing the draft changed the saved timetable')
 await page.getByRole('button', {name: '返回课程对话', exact: true}).click()
 await page.getByRole('button', {name: '放弃并关闭', exact: true}).click()
 await openSchedule.click()
 check((await title.inputValue()) === timetable[0].title, 'discarding the empty draft did not restore saved entries')
 await openMore()
 await clearSchedule.click()
 await confirmClear.click()
 await saveEmpty.click()
 await page.getByText('课表已保存，共 0 个课次。', {exact: true}).waitFor()
 await page.reload()
 await openSchedule.click()
 await upload.waitFor()
 check((await invoke<unknown[]>('study:get')).length === 0, 'saved empty timetable was not retained')
 const pdfiumFile =
  process.platform === 'win32' ? 'pdfium.dll' : process.platform === 'darwin' ? 'libpdfium.dylib' : 'libpdfium.so'
 if (statSync(join(target.resources, 'pdfium', pdfiumFile), {throwIfNoEntry: false})?.isFile()) {
  // Page one is a red JPEG 2000 scan. Page two uses a predefined Chinese CMap to draw blue text.
  await application.evaluate(({dialog}, file) => {
   dialog.showOpenDialog = async () => ({canceled: false, filePaths: [file]})
  }, resolve('scripts/fixtures/textbook-preview.pdf'))
  await page.getByRole('tab', {name: '教材划线', exact: true}).click()
  check((await guide.innerText()).includes('打开教材 PDF'), 'empty textbook guide must name the PDF action')
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
  // The registry keeps the same ID for a path. A re-open must still fetch the PDF bytes again.
  await openMore()
  await Promise.all([
   page.waitForRequest(request => new URL(request.url()).pathname.startsWith('/textbooks/')),
   page.getByRole('button', {name: '更换教材 PDF', exact: true}).click()
  ])
  await waitForPageColor(0)
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
