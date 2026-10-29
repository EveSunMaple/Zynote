import {_electron, type ElectronApplication} from 'playwright'
import {mkdtempSync, rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join, resolve} from 'node:path'

const appIndex = process.argv.indexOf('--app')
if (appIndex < 0 || !process.argv[appIndex + 1])
 throw new Error('Usage: node scripts/perf-smoke.mts --app <packaged executable>')
const executablePath = resolve(process.argv[appIndex + 1])
const dataRoot = mkdtempSync(join(tmpdir(), 'zynote-perf-'))
const maxStartupMs = Number(process.env.ZYNOTE_PERF_MAX_START_MS || 5000)
const maxMemoryMB = Number(process.env.ZYNOTE_PERF_MAX_MEMORY_MB || 800)
let application: ElectronApplication | undefined
try {
 const started = performance.now()
 application = await _electron.launch({
  executablePath,
  args: process.platform === 'linux' ? ['--no-sandbox'] : [],
  env: {...process.env, LISTENING_ASSISTANT_DATA: dataRoot}
 })
 await application.firstWindow({timeout: 45000})
 await application.firstWindow().then(page => page.waitForLoadState('domcontentloaded'))
 const windowReady = performance.now() - started
 const metrics = await application.evaluate(({app}) =>
  app.getAppMetrics().map(metric => ({
   type: metric.type,
   cpu: metric.cpu.percentCPUUsage,
   workingSetKB: metric.memory?.workingSetSize ?? 0
  }))
 )
 const memoryKB = metrics.reduce((sum, metric) => sum + metric.workingSetKB, 0)
 console.log(
  JSON.stringify({
   status: 'passed',
   windowReadyMs: Math.round(windowReady),
   memoryMB: Math.round(memoryKB / 1024),
   processes: metrics.length
  })
 )
} finally {
 await application?.close().catch(() => {})
 rmSync(dataRoot, {recursive: true, force: true})
}
