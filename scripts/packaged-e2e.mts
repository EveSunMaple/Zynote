import {_electron, type ElectronApplication} from 'playwright'
import {mkdtempSync, rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join, resolve} from 'node:path'

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
  page.evaluate(
   ({action, args}) =>
    (
     window as typeof window & {assistant: {invoke: (action: string, args?: unknown) => Promise<unknown>}}
    ).assistant.invoke(action, args),
   {action, args}
  ) as Promise<T>
 const info = await invoke<{version: string; platform: string}>('app:info')
 check(info.version === '0.0.1' && info.platform === process.platform, 'app:info mismatch')
 const health = await invoke<{ok: boolean; integrity: string}>('db:health')
 check(health.ok && health.integrity === 'ok', 'db:health mismatch')
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
 const update = await invoke<{phase: string}>('update:status')
 check(typeof update.phase === 'string', 'update:status mismatch')
 check(await invoke<boolean>('course:delete', {id: course.id, confirmed: true}), 'course:delete failed')
 check((await invoke<unknown[]>('courses')).length === 0, 'course was not deleted')
 console.log(JSON.stringify({status: 'passed', goCore: true, update: update.phase}))
} finally {
 await application?.close().catch(() => {})
 rmSync(dataRoot, {recursive: true, force: true})
}
