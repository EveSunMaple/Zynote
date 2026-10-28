import {_electron, type ElectronApplication} from 'playwright'
import {mkdtempSync, rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join, resolve} from 'node:path'

const appIndex = process.argv.indexOf('--app')
if (appIndex < 0 || !process.argv[appIndex + 1])
 throw new Error('Usage: node scripts/package-smoke.mts --app <packaged executable>')
const executablePath = resolve(process.argv[appIndex + 1])
const dataRoot = mkdtempSync(join(tmpdir(), 'zynote-package-smoke-'))
let application: ElectronApplication | undefined
try {
 application = await _electron.launch({
  executablePath,
  args: process.platform === 'linux' ? ['--no-sandbox'] : [],
  env: {...process.env, LISTENING_ASSISTANT_DATA: dataRoot}
 })
 const page = await application.firstWindow({timeout: 45000})
 await page.waitForLoadState('domcontentloaded')
 const info = (await page.evaluate(() =>
  (
   window as typeof window & {assistant: {invoke: (action: string, args?: unknown) => Promise<unknown>}}
  ).assistant.invoke('app:info')
 )) as {version?: string; platform?: string}
 if (info?.version !== '0.0.1' || info?.platform !== process.platform) throw new Error('packaged app:info mismatch')
 let options: unknown
 for (let attempt = 0; attempt < 30; attempt++) {
  try {
   // biome-ignore format: NOSONAR must stay on the await line
   options = await page.evaluate(() => // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
    (
     window as typeof window & {assistant: {invoke: (action: string, args?: unknown) => Promise<unknown>}}
    ).assistant.invoke('media:options')
   )
   break
  } catch {
   // biome-ignore format: NOSONAR must stay on the await line
   await new Promise(resolve => setTimeout(resolve, 500)) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  }
 }
 if (!options || typeof options !== 'object') throw new Error('packaged Go core did not answer media:options')
 const update = (await page.evaluate(() =>
  (
   window as typeof window & {assistant: {invoke: (action: string, args?: unknown) => Promise<unknown>}}
  ).assistant.invoke('update:status')
 )) as {currentVersion?: string; phase?: string}
 if (update?.currentVersion !== info.version || !update.phase) throw new Error('packaged update status mismatch')
 console.log(JSON.stringify({status: 'passed', version: info.version, platform: info.platform, goCore: true}))
} finally {
 await application?.close().catch(() => {})
 rmSync(dataRoot, {recursive: true, force: true})
}
