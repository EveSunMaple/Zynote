import * as fsPromises from 'node:fs/promises'
import {mkdtemp, mkdir, writeFile, realpath, rm, rename, symlink} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createHash} from 'node:crypto'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {ManagedPlayback} from './managed-playback'
import {mediaResponse} from './media-protocol'
vi.mock('node:fs/promises', async importOriginal => {
 const actual = await importOriginal<typeof import('node:fs/promises')>()
 return {...actual, open: vi.fn(actual.open)}
})
const taskID = '9e326025-30b4-45d0-bd27-4a30275adccb'
const requestID = '9e326025-30b4-45d0-bd27-4a30275adcc1'
const request2 = '9e326025-30b4-45d0-bd27-4a30275adcc2'
const request3 = '9e326025-30b4-45d0-bd27-4a30275adcc3'
const roots: string[] = [],
 managers: ManagedPlayback[] = []
afterEach(async () => {
 vi.useRealTimers()
 for (const manager of managers.splice(0)) await manager.dispose()
 for (const root of roots.splice(0)) await rm(root, {recursive: true, force: true, maxRetries: 5, retryDelay: 20})
})
async function fixture(
 size = 2 * 1024 * 1024 + 19,
 releaseCall?: (action: string, input: Record<string, unknown>) => Promise<unknown>
) {
 const root = await realpath(await mkdtemp(join(tmpdir(), 'zynote-playback-')))
 roots.push(root)
 const path = join(root, 'VideoTasks', taskID, 'video.mp4')
 await mkdir(join(root, 'VideoTasks', taskID), {recursive: true})
 const bytes = Buffer.alloc(size)
 for (let i = 0; i < bytes.length; i++) bytes[i] = i % 251
 await writeFile(path, bytes)
 const sha = createHash('sha256').update(bytes).digest('hex')
 const declaration = {
  version: 1,
  verification: 'declared',
  taskID,
  sourceKind: 'local',
  sourceIdentity: 'sha256:' + sha,
  sourceSHA256: sha,
  size: bytes.length,
  bindingRevision: 'a'.repeat(64)
 }
 const declare = vi.fn(async () => declaration)
 const manager = new ManagedPlayback(root, declare, releaseCall)
 managers.push(manager)
 const open = (id = requestID) => manager.open({requestID: id, videoTaskID: taskID, expectedSourceSHA256: sha})
 return {root, path, bytes, manager, open, declare, declaration, sha}
}
function deferred<T>() {
 let resolve!: (value: T) => void
 const promise = new Promise<T>(done => {
  resolve = done
 })
 return {promise, resolve}
}
describe('managed playback physical resource lifetime', () => {
 it('holds stream slots across cancelled reads until the actual read settles', async () => {
  const f = await fixture(),
   result = await f.open(),
   token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
  const pending = deferred<{bytesRead: number; buffer: Buffer}>()
  const read = vi.spyOn(token.file, 'read').mockImplementation(() => pending.promise as never)
  const first = f.manager.stream(token, 0, 9).getReader(),
   a = first.read().catch(() => undefined)
  const second = f.manager.stream(token, 0, 9).getReader(),
   b = second.read().catch(() => undefined)
  await Promise.resolve()
  await Promise.resolve()
  await first.cancel()
  await second.cancel()
  expect(() => f.manager.stream(token, 0, 9)).toThrow('媒体读取繁忙')
  pending.resolve({bytesRead: 0, buffer: Buffer.alloc(0)})
  await Promise.all([a, b])
  await new Promise(resolve => setImmediate(resolve))
  read.mockRestore()
 })
 it('returns timeout even while the metadata RPC never completes locally', async () => {
  const f = await fixture(),
   pending = deferred<typeof f.declaration>()
  f.declare.mockImplementation(() => pending.promise)
  vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
  let failure: unknown
  const opening = f.open().catch(error => {
   failure = error
  })
  await vi.advanceTimersByTimeAsync(120000)
  expect(failure).toMatchObject({code: 'TIMEOUT'})
  pending.resolve(f.declaration)
  await opening
 })
})
describe('managed playback verified bytes and request ownership', () => {
 it('serves verified ranges and keeps two request IDs isolated', async () => {
  const f = await fixture(),
   first = await f.open(),
   second = await f.open(request2)
  await expect(f.open(request3)).rejects.toMatchObject({code: 'BUSY'})
  const response = await mediaResponse(
   f.manager,
   new Request(first.tokenURL, {headers: {range: 'bytes=1048570-1048585'}})
  )
  expect(response.status).toBe(206)
  expect(Buffer.from(await response.arrayBuffer())).toEqual(f.bytes.subarray(1048570, 1048586))
  f.manager.close(requestID)
  expect((await mediaResponse(f.manager, new Request(first.tokenURL))).status).toBe(404)
  expect((await mediaResponse(f.manager, new Request(second.tokenURL, {method: 'HEAD'}))).status).toBe(200)
  await expect(f.open(request2)).rejects.toMatchObject({code: 'BUSY'})
 })
 it('rejects changed bytes before sending any changed block and pins path replacements', async () => {
  const f = await fixture(),
   result = await f.open()
  await rename(f.path, f.path + '.old')
  await writeFile(f.path, Buffer.alloc(f.bytes.length, 7))
  const old = await mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}}))
  expect(Buffer.from(await old.arrayBuffer())).toEqual(f.bytes.subarray(1, 10))
  await writeFile(f.path + '.old', Buffer.alloc(f.bytes.length, 8))
  await expect(
   (await mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}}))).arrayBuffer()
  ).rejects.toBeDefined()
  expect((await mediaResponse(f.manager, new Request(result.tokenURL))).status).toBe(404)
 })
})

describe('managed playback cancellation isolation', () => {
 it('keeps cancelled metadata RPC counted until it settles, without delaying the caller', async () => {
  const f = await fixture(),
   gate = deferred<typeof f.declaration>()
  f.declare.mockImplementation(() => gate.promise)
  const opening = f.open().catch(error => error)
  f.manager.close(requestID)
  expect(await opening).toMatchObject({code: 'CANCELLED'})
  await expect(f.open(request2)).rejects.toMatchObject({code: 'BUSY'})
  await expect(f.open(request3)).rejects.toMatchObject({code: 'BUSY'})
  expect(f.declare).toHaveBeenCalledTimes(1)
  gate.resolve(f.declaration)
  await new Promise(resolve => setImmediate(resolve))
  f.declare.mockResolvedValue(f.declaration)
  const second = await f.open(request2)
  expect((await mediaResponse(f.manager, new Request(second.tokenURL, {method: 'HEAD'}))).status).toBe(200)
 })
 it('propagates Request abort with no consumption, and cancels one pending range without revoking the token', async () => {
  const f = await fixture(),
   result = await f.open(),
   token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
  const emptyAbort = new AbortController()
  await mediaResponse(f.manager, new Request(result.tokenURL, {signal: emptyAbort.signal}))
  expect(token.streams.size).toBe(1)
  emptyAbort.abort()
  expect(token.streams.size).toBe(0)
  const gate = deferred<{bytesRead: number; buffer: Buffer}>(),
   spy = vi.spyOn(token.file, 'read').mockImplementation(() => gate.promise as never)
  const abort = new AbortController(),
   response = await mediaResponse(
    f.manager,
    new Request(result.tokenURL, {signal: abort.signal, headers: {range: 'bytes=0-9'}})
   )
  const reading = response.arrayBuffer().catch(error => error)
  await Promise.resolve()
  await Promise.resolve()
  abort.abort()
  expect(token.streams.size).toBe(1)
  gate.resolve({bytesRead: 0, buffer: Buffer.alloc(0)})
  await reading
  await new Promise(resolve => setImmediate(resolve))
  spy.mockRestore()
  const next = await mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=8-15'}}))
  expect(Buffer.from(await next.arrayBuffer())).toEqual(f.bytes.subarray(8, 16))
 })
 it('counts a revoked handle until its deferred physical close settles', async () => {
  const a = await fixture(),
   b = await fixture(),
   c = await fixture()
  const first = await a.open()
  await a.open(request2)
  await b.open()
  await b.open(request2)
  const token = a.manager.get(first.tokenURL.split('/').at(-1)!)!,
   gate = deferred<void>()
  const actual = token.file.close.bind(token.file)
  vi.spyOn(token.file, 'close').mockImplementation(async () => {
   await gate.promise
   await actual()
  })
  a.manager.close(requestID)
  await expect(a.open(request3)).rejects.toMatchObject({code: 'BUSY'})
  await expect(c.open()).rejects.toMatchObject({code: 'BUSY'})
  gate.resolve()
  await new Promise(resolve => setImmediate(resolve))
  await new Promise(resolve => setImmediate(resolve))
  await vi.waitFor(async () => {
   const third = await c.open()
   expect(third.sourceSHA256).toBe(c.sha)
  })
 })
})

it('does not emit an in-flight block after the exact absolute token expiry', async () => {
 const f = await fixture(),
  result = await f.open(),
  token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
 const gate = deferred<{bytesRead: number; buffer: Buffer}>(),
  spy = vi.spyOn(token.file, 'read').mockImplementation(() => gate.promise as never)
 const response = await mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=0-9'}}))
 const reading = response.arrayBuffer().catch(error => error)
 await Promise.resolve()
 await Promise.resolve()
 vi.useFakeTimers({toFake: ['Date']})
 vi.setSystemTime(token.created + 30 * 60 * 1000)
 gate.resolve({bytesRead: 1024 * 1024, buffer: Buffer.alloc(0)})
 expect(await reading).toMatchObject({code: 'CANCELLED'})
 spy.mockRestore()
 expect((await mediaResponse(f.manager, new Request(result.tokenURL))).status).toBe(404)
})

it('rejects metadata drift and managed directory aliases without minting a token', async () => {
 const f = await fixture()
 f.declare
  .mockResolvedValueOnce(f.declaration)
  .mockResolvedValueOnce({...f.declaration, bindingRevision: 'b'.repeat(64)})
 await expect(f.open()).rejects.toMatchObject({code: 'CONFLICT'})
 await rename(join(f.root, 'VideoTasks'), join(f.root, 'ActualVideoTasks'))
 await symlink(join(f.root, 'ActualVideoTasks'), join(f.root, 'VideoTasks'), 'junction')
 f.declare.mockResolvedValue(f.declaration)
 await expect(f.open(request2)).rejects.toMatchObject({code: 'UNAVAILABLE'})
})

for (const kind of ['root', 'VideoTasks', 'task', 'video'] as const) {
 it(`rejects ${kind} alias before opening any outside target`, async context => {
  const f = await fixture(),
   external = await realpath(await mkdtemp(join(tmpdir(), 'zynote-outside-')))
  roots.push(external)
  const outside = join(external, 'video.mp4')
  await writeFile(outside, Buffer.alloc(f.bytes.length, 42))
  let library = f.root
  if (kind === 'root') {
   library = join(external, 'library-alias')
   await symlink(f.root, library, 'junction')
  } else if (kind === 'VideoTasks') {
   await rename(join(f.root, 'VideoTasks'), join(f.root, 'HeldTasks'))
   await symlink(external, join(f.root, 'VideoTasks'), 'junction')
  } else if (kind === 'task') {
   await rename(join(f.root, 'VideoTasks', taskID), join(f.root, 'VideoTasks', 'held'))
   await symlink(external, join(f.root, 'VideoTasks', taskID), 'junction')
  } else {
   await rename(f.path, f.path + '.held')
   try {
    await symlink(outside, f.path, 'file')
   } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'EPERM') context.skip('File symlink requires OS privilege')
    throw error
   }
  }
  const manager = new ManagedPlayback(library, async () => f.declaration)
  managers.push(manager)
  const opening = vi.mocked(fsPromises.open)
  opening.mockClear()
  try {
   await expect(manager.open({requestID, videoTaskID: taskID, expectedSourceSHA256: f.sha})).rejects.toMatchObject({
    code: 'UNAVAILABLE'
   })
   expect(opening).not.toHaveBeenCalled()
  } finally {
   opening.mockClear()
  }
  expect((await fsPromises.readFile(outside)).equals(Buffer.alloc(f.bytes.length, 42))).toBe(true)
 })
}

it('rechecks expiry immediately before enqueue after a deferred stat crosses TTL', async () => {
 const f = await fixture(),
  result = await f.open(),
  token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
 const original = await token.file.stat(),
  gate = deferred<typeof original>()
 const stat = vi.spyOn(token.file, 'stat').mockImplementationOnce(() => gate.promise)
 const reading = (await mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}})))
  .arrayBuffer()
  .catch(error => error)
 await vi.waitFor(() => expect(stat).toHaveBeenCalled())
 vi.useFakeTimers({toFake: ['Date']})
 vi.setSystemTime(token.created + 30 * 60 * 1000)
 gate.resolve(original)
 expect(await reading).toMatchObject({code: 'CANCELLED'})
 stat.mockRestore()
 vi.useRealTimers()
})

describe('managed playback global and time budgets', () => {
 it('rejects the fifth physical stream across tokens and releases cancelled idle streams', async () => {
  const a = await fixture(),
   b = await fixture(),
   c = await fixture()
  const resultA = await a.open(),
   resultB = await b.open(),
   resultC = await c.open()
  const tokenA = a.manager.get(resultA.tokenURL.split('/').at(-1)!)!,
   tokenB = b.manager.get(resultB.tokenURL.split('/').at(-1)!)!,
   tokenC = c.manager.get(resultC.tokenURL.split('/').at(-1)!)!
  const streams = [
   a.manager.stream(tokenA, 0, 9),
   a.manager.stream(tokenA, 0, 9),
   b.manager.stream(tokenB, 0, 9),
   b.manager.stream(tokenB, 0, 9)
  ]
  expect(() => c.manager.stream(tokenC, 0, 9)).toThrow('媒体读取繁忙')
  await Promise.all(streams.map(stream => stream.cancel()))
  await c.manager.stream(tokenC, 0, 9).cancel()
 })
 it('expires tokens at exactly idle5min or absolute30min without the periodic sweep', async () => {
  vi.useRealTimers()
  const f = await fixture(),
   result = await f.open(),
   token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
  vi.useFakeTimers({toFake: ['Date']})
  vi.setSystemTime(token.created + 5 * 60 * 1000)
  expect(f.manager.get(token.id)).toBeUndefined()
  vi.useRealTimers()
  const next = await f.open(request2),
   second = f.manager.get(next.tokenURL.split('/').at(-1)!)!
  vi.useFakeTimers({toFake: ['Date']})
  second.touched = second.created + 29 * 60 * 1000
  vi.setSystemTime(second.created + 30 * 60 * 1000)
  expect(f.manager.get(second.id)).toBeUndefined()
 })
 it('ends an idle stream after5min without aborting a progressing long stream', async () => {
  const f = await fixture(12 * 1024 * 1024),
   result = await f.open(),
   token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
  vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
  const idle = f.manager.stream(token, 0, 9),
   idleReader = idle.getReader()
  void idleReader.closed.catch(() => {})
  await vi.advanceTimersByTimeAsync(5 * 60 * 1000)
  await expect(idleReader.read()).rejects.toMatchObject({code: 'TIMEOUT'})
  const reader = f.manager.stream(token, 0, token.size - 1).getReader()
  void reader.closed.catch(() => {})
  await reader.read()
  for (let index = 0; index < 20; index++) {
   await vi.advanceTimersByTimeAsync(11999)
   await reader.read()
  }
  const next = await mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=2-9'}}))
  expect(Buffer.from(await next.arrayBuffer()).equals(f.bytes.subarray(2, 10))).toBe(true)
 })
 it('revokes a token before crossing its cumulative32GiB read budget without reading32GiB', async () => {
  const f = await fixture(),
   result = await f.open(),
   token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
  token.bytes = 32 * 1024 ** 3 - 1
  const read = vi.spyOn(token.file, 'read')
  expect(() => f.manager.stream(token, 0, 9)).toThrow('托管媒体不可用')
  expect(read).not.toHaveBeenCalled()
  read.mockRestore()
  expect(f.manager.get(token.id)).toBeUndefined()
 })
})

it.skipIf(process.platform !== 'win32')(
 'accepts real Windows path case variants while retaining alias rejection',
 async () => {
  const f = await fixture(),
   manager = new ManagedPlayback(f.root.toUpperCase(), async () => f.declaration)
  managers.push(manager)
  const result = await manager.open({requestID, videoTaskID: taskID, expectedSourceSHA256: f.sha})
  expect(
   Buffer.from(
    await (await mediaResponse(manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}}))).arrayBuffer()
   ).equals(f.bytes.subarray(1, 10))
  ).toBe(true)
 }
)

it.skipIf(process.platform !== 'win32')(
 'rejects UNC, device and ADS roots before any declaration RPC or filesystem access',
 async () => {
  for (const root of [
   String.raw`\\no-network.invalid\share`,
   String.raw`\\?\C:\Library`,
   String.raw`C:\Library:stream`
  ]) {
   const declaration = vi.fn(async () => {
    throw new Error('must not call metadata')
   })
   const manager = new ManagedPlayback(root, declaration)
   managers.push(manager)
   const open = vi.mocked(fsPromises.open)
   open.mockClear()
   await expect(
    manager.open({requestID, videoTaskID: taskID, expectedSourceSHA256: 'a'.repeat(64)})
   ).rejects.toMatchObject({code: 'UNAVAILABLE'})
   expect(declaration).not.toHaveBeenCalled()
   expect(open).not.toHaveBeenCalled()
  }
 }
)

const releaseSHA = 'b'.repeat(64)
function releaseReply(input: Record<string, unknown>, state: 'prepared' | 'released' = 'prepared') {
 return {
  plan: {
   version: 1,
   id: input.id,
   refKind: input.expectedRefKind,
   refSHA256: input.expectedRefSHA256,
   releaseID: input.releaseID,
   state,
   files: []
  },
  tombstoneSHA256: releaseSHA
 }
}
const releaseArgs = {videoTaskID: taskID, expectedRefKind: 'intent', expectedRefSHA256: 'c'.repeat(64)}
describe('managed playback fenced release', () => {
 it('fences every owner synchronously, awaits pending metadata and reuses original release identity', async () => {
  const ready = deferred<unknown>(),
   prepare = deferred<unknown>()
  const release = vi.fn(async (action: string, input: Record<string, unknown>) =>
   action.includes('prepare-') ? prepare.promise : releaseReply(input, 'released')
  )
  const f = await fixture(1024, release)
  const other = new ManagedPlayback(f.root, async () => ready.promise)
  managers.push(other)
  const opening = other
   .open({requestID: request2, videoTaskID: taskID, expectedSourceSHA256: f.sha})
   .catch(error => error)
  const releasing = f.manager.release(releaseArgs)
  await expect(f.open()).rejects.toMatchObject({code: 'BUSY'})
  await expect(
   other.open({requestID: request3, videoTaskID: taskID, expectedSourceSHA256: f.sha})
  ).rejects.toMatchObject({code: 'BUSY'})
  const candidate = release.mock.calls[0][1]
  const originalID = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
  prepare.resolve(releaseReply({...candidate, releaseID: originalID}))
  await new Promise(resolve => setImmediate(resolve))
  expect(release).toHaveBeenCalledTimes(1)
  ready.resolve(f.declaration)
  expect(await opening).toMatchObject({code: 'CANCELLED'})
  await expect(releasing).resolves.toEqual({videoTaskID: taskID, state: 'released'})
  expect(release.mock.calls[1]).toEqual([
   'video-ppt:finalize-playback-release',
   {...candidate, releaseID: originalID, expectedTombstoneSHA256: releaseSHA}
  ])
 })
 it('awaits cancelled physical read and actual close before finalize, without revoking another task', async () => {
  const release = vi.fn(async (action: string, input: Record<string, unknown>) =>
   releaseReply(input, action.includes('prepare-') ? 'prepared' : 'released')
  )
  const f = await fixture(1024, release),
   opened = await f.open(),
   token = f.manager.get(opened.tokenURL.split('/').at(-1)!)!
  const task2 = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
  await mkdir(join(f.root, 'VideoTasks', task2))
  await writeFile(join(f.root, 'VideoTasks', task2, 'video.mp4'), f.bytes)
  const other = new ManagedPlayback(f.root, async () => ({...f.declaration, taskID: task2}))
  managers.push(other)
  const untouched = await other.open({requestID: request2, videoTaskID: task2, expectedSourceSHA256: f.sha})
  const read = deferred<{bytesRead: number; buffer: Buffer}>(),
   close = deferred<void>()
  const realClose = token.file.close.bind(token.file)
  const readSpy = vi.spyOn(token.file, 'read').mockImplementation(() => read.promise as never)
  const closeSpy = vi.spyOn(token.file, 'close').mockImplementation(async () => {
   await close.promise
   await realClose()
  })
  const reading = f.manager
   .stream(token, 0, 9)
   .getReader()
   .read()
   .catch(() => undefined)
  await Promise.resolve()
  await Promise.resolve()
  const releasing = f.manager.release(releaseArgs)
  await new Promise(resolve => setImmediate(resolve))
  expect(release).toHaveBeenCalledTimes(1)
  read.resolve({bytesRead: 0, buffer: Buffer.alloc(0)})
  await reading
  await new Promise(resolve => setImmediate(resolve))
  expect(closeSpy).toHaveBeenCalledTimes(1)
  expect(release).toHaveBeenCalledTimes(1)
  expect(other.get(untouched.tokenURL.split('/').at(-1)!)).toBeDefined()
  close.resolve()
  await releasing
  expect(release).toHaveBeenCalledTimes(2)
  readSpy.mockRestore()
  closeSpy.mockRestore()
 })
 it.each(['prepare', 'finalize'])(
  'keeps mint fence after UNKNOWN %s, but explicit release can reconcile',
  async phase => {
   let fail = true
   const release = vi.fn(async (action: string, input: Record<string, unknown>) => {
    if (fail && action.includes(phase + '-'))
     throw Object.assign(new Error('RPC disconnected after durable write'), {code: 'UNAVAILABLE'})
    return releaseReply(input, action.includes('prepare-') ? 'prepared' : 'released')
   })
   const f = await fixture(1024, release)
   await expect(f.manager.release(releaseArgs)).rejects.toThrow('RPC disconnected')
   await expect(f.open()).rejects.toMatchObject({code: 'BUSY'})
   fail = false
   await expect(f.manager.release(releaseArgs)).resolves.toMatchObject({state: 'released'})
  }
 )
 it('retains fence while deferred RPC rejects after prepare-write and permits reconciliation', async () => {
  let reject!: (reason: unknown) => void
  const failed = new Promise((_, no) => {
   reject = no
  })
  let first = true
  const release = vi.fn(async (action: string, input: Record<string, unknown>) => {
   if (first) {
    first = false
    return failed
   }
   return releaseReply(input, action.includes('prepare-') ? 'prepared' : 'released')
  })
  const f = await fixture(1024, release)
  const releasing = f.manager.release(releaseArgs)
  await expect(f.open()).rejects.toMatchObject({code: 'BUSY'})
  reject(new Error('durable plan persisted, reply lost'))
  await expect(releasing).rejects.toThrow('reply lost')
  await expect(f.open()).rejects.toMatchObject({code: 'BUSY'})
  await f.manager.release(releaseArgs)
 })
 it('does not finalize or free failed-close quota; disposed owner remains visible for controlled retry', async () => {
  const release = vi.fn(async (action: string, input: Record<string, unknown>) =>
   releaseReply(input, action.includes('prepare-') ? 'prepared' : 'released')
  )
  const f = await fixture(1024, release),
   opened = await f.open(),
   token = f.manager.get(opened.tokenURL.split('/').at(-1)!)!
  const realClose = token.file.close.bind(token.file)
  let fail = true
  const closeSpy = vi.spyOn(token.file, 'close').mockImplementation(async () => {
   if (fail) throw new Error('close unconfirmed')
   await realClose()
  })
  await expect(f.manager.release(releaseArgs)).rejects.toThrow('close unconfirmed')
  expect(release.mock.calls.every(([action]) => action.includes('prepare-'))).toBe(true)
  await expect(f.open()).rejects.toMatchObject({code: 'BUSY'})
  // The failed physical reservation still consumes the owner's two-slot quota.
  const otherTask = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
  await mkdir(join(f.root, 'VideoTasks', otherTask))
  await writeFile(join(f.root, 'VideoTasks', otherTask, 'video.mp4'), f.bytes)
  f.declare.mockImplementation(async () => ({...f.declaration, taskID: otherTask}))
  const kept = await f.manager.open({requestID: request2, videoTaskID: otherTask, expectedSourceSHA256: f.sha})
  await expect(
   f.manager.open({requestID: request3, videoTaskID: otherTask, expectedSourceSHA256: f.sha})
  ).rejects.toMatchObject({code: 'BUSY'})
  f.manager.close(request2)
  await f.manager.dispose()
  const reconciler = new ManagedPlayback(f.root, async () => f.declaration, release)
  managers.push(reconciler)
  await expect(reconciler.release(releaseArgs)).rejects.toThrow('close unconfirmed')
  expect(release.mock.calls.some(([action]) => action.includes('finalize-'))).toBe(false)
  fail = false
  await expect(reconciler.release(releaseArgs)).resolves.toMatchObject({state: 'released'})
  expect(closeSpy).toHaveBeenCalledTimes(4)
  expect(kept).toBeDefined()
  closeSpy.mockRestore()
 })
 it('rejects changed finalize plan and keeps mint blocked', async () => {
  const release = vi.fn(async (action: string, input: Record<string, unknown>) => {
   const reply = releaseReply(input, action.includes('prepare-') ? 'prepared' : 'released')
   if (action.includes('finalize-')) reply.plan.files = [{name: 'video.mp4', size: 1, sha256: 'a'.repeat(64)}] as never
   return reply
  })
  const f = await fixture(1024, release)
  await expect(f.manager.release(releaseArgs)).rejects.toMatchObject({code: 'CONFLICT'})
  await expect(f.open()).rejects.toMatchObject({code: 'BUSY'})
 })
})

it('owns a handle before failing post-open stat; rejected cleanup stays indexed and charged', async () => {
 const release = vi.fn(async (action: string, input: Record<string, unknown>) =>
  releaseReply(input, action.includes('prepare-') ? 'prepared' : 'released')
 )
 const f = await fixture(1024, release)
 const realOpen = vi.mocked(fsPromises.open).getMockImplementation()!
 let restore = () => {}
 vi.mocked(fsPromises.open).mockImplementationOnce(async (...args) => {
  const file = await realOpen(...args)
  const stat = vi.spyOn(file, 'stat').mockRejectedValue(new Error('post-open stat failed'))
  const close = vi.spyOn(file, 'close').mockRejectedValue(new Error('post-open close unconfirmed'))
  restore = () => {
   stat.mockRestore()
   close.mockRestore()
  }
  return file
 })
 await expect(f.open()).rejects.toThrow('post-open close unconfirmed')
 await expect(f.manager.release(releaseArgs)).rejects.toThrow('post-open close unconfirmed')
 expect(release.mock.calls.some(([action]) => action.includes('finalize-'))).toBe(false)
 const otherTask = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
 await mkdir(join(f.root, 'VideoTasks', otherTask))
 await writeFile(join(f.root, 'VideoTasks', otherTask, 'video.mp4'), f.bytes)
 f.declare.mockImplementation(async () => ({...f.declaration, taskID: otherTask}))
 await f.manager.open({requestID: request2, videoTaskID: otherTask, expectedSourceSHA256: f.sha})
 await expect(
  f.manager.open({requestID: request3, videoTaskID: otherTask, expectedSourceSHA256: f.sha})
 ).rejects.toMatchObject({code: 'BUSY'})
 restore()
 await expect(f.manager.release(releaseArgs)).resolves.toMatchObject({state: 'released'})
})

it('rolls back an initial confirmed no-mutation BUSY, but never clears a preceding UNKNOWN fence', async () => {
 let phase: 'busy' | 'unknown' | 'success' = 'busy'
 const release = vi.fn(async (action: string, input: Record<string, unknown>) => {
  if (phase === 'unknown') throw new Error('reply lost')
  if (phase === 'busy') return {status: 'rejected', mutation: 'none', code: 'BUSY', message: 'active callback'}
  return releaseReply(input, action.includes('prepare-') ? 'prepared' : 'released')
 })
 const f = await fixture(1024, release)
 await expect(f.manager.release(releaseArgs)).rejects.toMatchObject({code: 'BUSY'})
 const opened = await f.open()
 f.manager.close(opened.requestID)
 phase = 'unknown'
 await expect(f.manager.release(releaseArgs)).rejects.toThrow('reply lost')
 phase = 'busy'
 await expect(f.manager.release(releaseArgs)).rejects.toMatchObject({code: 'BUSY'})
 await expect(f.open(request2)).rejects.toMatchObject({code: 'BUSY'})
 phase = 'success'
 await f.manager.release(releaseArgs)
})
