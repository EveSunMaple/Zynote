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
 } else if (frame.method === 'fixture.frames') {
  for (const output of frame.params.frames) process.stdout.write(JSON.stringify(output) + '\\n');
  send({method: 'event', params: {name: 'received', action: frame.method}});
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
 it('keeps long operations alive past 10s and bounds private bound playback independently', async () => {
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
   'video-ppt:finalize-playback-release',
   'video-ppt:summary-from-retained'
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

  let defaultError: unknown, slowError: unknown, longError: unknown, boundError: unknown, boundCheckError: unknown
  const bound = core.call('video-ppt:bound-playback-source', {}).catch(error => {
   boundError = error
  })
  const boundCheck = core.call('video-ppt:bound-playback-check', {}).catch(error => {
   boundCheckError = error
  })
  const ordinary = core.call('health', {}).catch(error => {
   defaultError = error
  })
  const slow = core.call('subtitle-derived:audit-publication', {}).catch(error => {
   slowError = error
  })
  const long = core.call('subtitle-derived:start', {}).catch(error => {
   longError = error
  })
  await until(() => received.length === actions.length + 5)
  await vi.advanceTimersByTimeAsync(5999)
  expect(boundError).toBeUndefined()
  expect(boundCheckError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(boundError).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
  expect(boundCheckError).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
  expect(defaultError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(3999)
  expect(defaultError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(defaultError).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
  expect(slowError).toBeUndefined()
  expect(longError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(79999)
  expect(slowError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(slowError).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
  expect(longError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1709999)
  expect(longError).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(longError).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
  await Promise.all([bound, boundCheck, ordinary, slow, long])

  const malformed = (id: string) => [
   null,
   [],
   {jsonrpc: '2.0', id: id, method: '', result: {}},
   {jsonrpc: '2.0', id: id, method: null, result: {}},
   {jsonrpc: '2.0', id: id, method: 0, result: {}},
   {jsonrpc: '2.0', id: id, result: {}, error: {code: 'CONFLICT', message: 'bad'}},
   {jsonrpc: '2.0', id: id, error: null},
   {jsonrpc: '2.0', id: id, error: 'bad'},
   {jsonrpc: '2.0', id: id, error: {code: 1, message: 'bad'}},
   {jsonrpc: '2.0', id: id, error: {code: 'CONFLICT', message: 1}}
  ]
  const validation = core.callTracked('video-ppt:bound-playback-source', {})
  let validationResult = false,
   validationPhysical = false
  void validation.result.then(() => {
   validationResult = true
  })
  void validation.physicalSettled.then(() => {
   validationPhysical = true
  })
  let checkpoint = received.length
  await until(() => received.length === checkpoint + 1)
  checkpoint = received.length
  fixture.child!.stdin!.write(
   JSON.stringify({method: 'fixture.frames', params: {frames: malformed(validation.requestID)}}) + '\n'
  )
  await until(() => received.length === checkpoint + 1)
  expect(validationResult).toBe(false)
  expect(validationPhysical).toBe(false)
  fixture.child!.stdin!.write(JSON.stringify({method: 'fixture.release'}) + '\n')
  await Promise.all([validation.result, validation.physicalSettled])
  expect(validationResult).toBe(true)
  expect(validationPhysical).toBe(true)

  const tracked = core.callTracked('video-ppt:bound-playback-source', {})
  let logicalError: unknown,
   physical = false
  const logical = tracked.result.catch(error => {
   logicalError = error
  })
  void tracked.physicalSettled.then(() => {
   physical = true
  })
  checkpoint = received.length
  await until(() => received.length === checkpoint + 1)
  await vi.advanceTimersByTimeAsync(6000)
  await logical
  expect(logicalError).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
  expect(physical).toBe(false)
  const originalChild = fixture.child!

  checkpoint = received.length
  originalChild.stdin!.write(
   JSON.stringify({method: 'fixture.frames', params: {frames: malformed(tracked.requestID)}}) + '\n'
  )
  await until(() => received.length === checkpoint + 1)
  expect(physical).toBe(false)
  // A new actual child generation cannot terminate the old child's same ID.
  core.start()
  await until(() => core?.currentStatus === 'ready')
  checkpoint = received.length
  fixture.child!.stdin!.write(
   JSON.stringify({method: 'fixture.frames', params: {frames: [{jsonrpc: '2.0', id: tracked.requestID, result: {}}]}}) +
    '\n'
  )
  await until(() => received.length === checkpoint + 1)
  expect(physical).toBe(false)
  originalChild.stdin!.write(JSON.stringify({method: 'fixture.release'}) + '\n')
  await tracked.physicalSettled
  expect(physical).toBe(true)
  const originalExited = new Promise<void>(resolve => originalChild.once('exit', () => resolve()))
  originalChild.kill()
  await originalExited

  const exiting = core.callTracked('video-ppt:bound-playback-source', {})
  void exiting.result.catch(() => {})
  let exitedPhysically = false
  void exiting.physicalSettled.then(() => {
   exitedPhysically = true
  })
  checkpoint = received.length
  await until(() => received.length === checkpoint + 1)
  const child = fixture.child!
  child.emit('error', new Error('controlled nonterminal child error'))
  await Promise.resolve()
  expect(exitedPhysically).toBe(false)
  core.dispose()
  await Promise.resolve()
  expect(exitedPhysically).toBe(false)
  child.kill()
  await exiting.physicalSettled
  expect(exitedPhysically).toBe(true)
 }, 15000)
})
