import type {ChildProcess} from 'node:child_process'
import {setTimeout as realDelay} from 'node:timers/promises'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {GoCore} from './go-core'

const fixture = vi.hoisted(() => ({child: undefined as ChildProcess | undefined}))
vi.mock('electron', () => ({app: {isPackaged: false, getAppPath: () => '/controlled-fixture'}}))
vi.mock('node:fs', async importOriginal => ({
 ...(await importOriginal<typeof import('node:fs')>()),
 existsSync: () => true
}))
vi.mock('node:child_process', async importOriginal => {
 const actual = await importOriginal<typeof import('node:child_process')>()
 return {
  ...actual,
  spawn: () => {
   fixture.child = actual.spawn(
    process.execPath,
    [
     '-e',
     `
const readline = require('node:readline');
const pending = [];
const send = frame => process.stdout.write(JSON.stringify({jsonrpc: '2.0', ...frame}) + '\\n');
readline.createInterface({input: process.stdin}).on('line', line => {
 const frame = JSON.parse(line);
 if (frame.method === 'core.hello') send({id: frame.id, result: {}});
 else if (frame.method === 'fixture.release') {
  for (const request of pending.splice(0)) send({id: request.id, result: {action: request.method}});
 } else {
  pending.push(frame);
  send({method: 'event', params: {name: 'received', action: frame.method}});
 }
});
`
    ],
    {stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true}
   )
   return fixture.child
  }
 }
})

let core: GoCore | undefined
async function until(predicate: () => boolean) {
 for (let attempt = 0; attempt < 500; attempt++) {
  if (predicate()) return
  await realDelay(10)
 }
 throw new Error('Controlled JSON-RPC child did not respond')
}
afterEach(async () => {
 vi.useRealTimers()
 const child = fixture.child
 const exited =
  child && child.exitCode === null && child.signalCode === null
   ? new Promise<void>(resolve => child.once('exit', () => resolve()))
   : Promise.resolve()
 core?.dispose()
 await exited
 core = undefined
 fixture.child = undefined
})

// Exercise the real GoCore framing, pending map and clock against isolated Node pipes.
describe('GoCore request timeouts', () => {
 it('keeps sixteen corrected long operations alive past 10s and preserves all three timeout budgets', async () => {
  const received: string[] = []
  core = new GoCore({
   appVersion: 'test',
   dataRoot: '',
   configRoot: '',
   appRoot: '',
   libraryRoot: '',
   previousLibraryRoot: '',
   resourcesRoot: '',
   promptsRoot: '',
   nativePath: '',
   portable: false,
   services: {},
   onEvent: event => received.push((event as unknown as {action: string}).action),
   onFailed: vi.fn()
  })
  core.start()
  await until(() => core?.currentStatus === 'ready')
  vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
  const actions = [
   'subtitle-derived:start',
   'subtitle-derived:resume',
   'subtitle-sync:attach',
   'subtitle-sync:mixed',
   'subtitle-sync:resume',
   'subtitle-sync:start',
   'visual:prepare-resume',
   'visual:recover-output',
   'knowledge:vector-rebuild',
   'knowledge:classification-suggest',
   'practice:answer',
   'textbook:manual',
   'textbook:clear',
   'textbook:apply',
   'video-ppt:prepare-playback-release',
   'video-ppt:finalize-playback-release'
  ]
  let completed = 0
  const requests = actions.map(action =>
   core!.call(action, {}).then(result => {
    completed++
    return result
   })
  )
  const allRequests = Promise.all(requests)
  void allRequests.catch(() => {})
  await until(() => received.length === actions.length)
  await vi.advanceTimersByTimeAsync(10001)
  expect(completed).toBe(0)
  fixture.child!.stdin!.write(JSON.stringify({method: 'fixture.release'}) + '\n')
  expect(await allRequests).toEqual(actions.map(action => ({action})))
  expect(completed).toBe(actions.length)

  let defaultError: unknown, slowError: unknown, longError: unknown
  const ordinary = core.call('health', {}).catch(error => {
   defaultError = error
  })
  const slow = core.call('subtitle-derived:audit-publication', {}).catch(error => {
   slowError = error
  })
  const long = core.call('subtitle-derived:start', {}).catch(error => {
   longError = error
  })
  await until(() => received.length === actions.length + 3)
  await vi.advanceTimersByTimeAsync(9999)
  expect(defaultError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(defaultError).toEqual(new Error('Go 后端请求超时'))
  expect(slowError).toBeUndefined()
  expect(longError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(79999)
  expect(slowError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(slowError).toEqual(new Error('Go 后端请求超时'))
  expect(longError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1709999)
  expect(longError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(longError).toEqual(new Error('Go 后端请求超时'))
  await Promise.all([ordinary, slow, long])
 }, 15000)
})
