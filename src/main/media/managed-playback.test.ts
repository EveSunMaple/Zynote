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
async function fixture(size = 2 * 1024 * 1024 + 19) {
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
 const manager = new ManagedPlayback(root, declare)
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
  const response = mediaResponse(f.manager, new Request(first.tokenURL, {headers: {range: 'bytes=1048570-1048585'}}))
  expect(response.status).toBe(206)
  expect(Buffer.from(await response.arrayBuffer())).toEqual(f.bytes.subarray(1048570, 1048586))
  f.manager.close(requestID)
  expect(mediaResponse(f.manager, new Request(first.tokenURL)).status).toBe(404)
  expect(mediaResponse(f.manager, new Request(second.tokenURL, {method: 'HEAD'})).status).toBe(200)
  await expect(f.open(request2)).rejects.toMatchObject({code: 'BUSY'})
 })
 it('rejects changed bytes before sending any changed block and pins path replacements', async () => {
  const f = await fixture(),
   result = await f.open()
  await rename(f.path, f.path + '.old')
  await writeFile(f.path, Buffer.alloc(f.bytes.length, 7))
  const old = mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}}))
  expect(Buffer.from(await old.arrayBuffer())).toEqual(f.bytes.subarray(1, 10))
  await writeFile(f.path + '.old', Buffer.alloc(f.bytes.length, 8))
  await expect(
   mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}})).arrayBuffer()
  ).rejects.toBeDefined()
  expect(mediaResponse(f.manager, new Request(result.tokenURL)).status).toBe(404)
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
  expect(mediaResponse(f.manager, new Request(second.tokenURL, {method: 'HEAD'})).status).toBe(200)
 })
 it('propagates Request abort with no consumption, and cancels one pending range without revoking the token', async () => {
  const f = await fixture(),
   result = await f.open(),
   token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
  const emptyAbort = new AbortController()
  mediaResponse(f.manager, new Request(result.tokenURL, {signal: emptyAbort.signal}))
  expect(token.streams.size).toBe(1)
  emptyAbort.abort()
  expect(token.streams.size).toBe(0)
  const gate = deferred<{bytesRead: number; buffer: Buffer}>(),
   spy = vi.spyOn(token.file, 'read').mockImplementation(() => gate.promise as never)
  const abort = new AbortController(),
   response = mediaResponse(
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
  const next = mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=8-15'}}))
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
 const response = mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=0-9'}}))
 const reading = response.arrayBuffer().catch(error => error)
 await Promise.resolve()
 await Promise.resolve()
 vi.useFakeTimers({toFake: ['Date']})
 vi.setSystemTime(token.created + 30 * 60 * 1000)
 gate.resolve({bytesRead: 1024 * 1024, buffer: Buffer.alloc(0)})
 expect(await reading).toMatchObject({code: 'CANCELLED'})
 spy.mockRestore()
 expect(mediaResponse(f.manager, new Request(result.tokenURL)).status).toBe(404)
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
 const reading = mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}}))
  .arrayBuffer()
  .catch(error => error)
 await vi.waitFor(() => expect(stat).toHaveBeenCalled())
 vi.useFakeTimers({toFake: ['Date']})
 vi.setSystemTime(token.created + 30 * 60 * 1000)
 gate.resolve(original)
 expect(await reading).toMatchObject({code: 'CANCELLED'})
 stat.mockRestore()
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
 it('ends an idle stream at15s and a progressing stream at120s while keeping healthy tokens seekable', async () => {
  const f = await fixture(12 * 1024 * 1024),
   result = await f.open(),
   token = f.manager.get(result.tokenURL.split('/').at(-1)!)!
  vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
  const idle = f.manager.stream(token, 0, 9),
   idleReader = idle.getReader()
  void idleReader.closed.catch(() => {})
  await vi.advanceTimersByTimeAsync(15000)
  await expect(idleReader.read()).rejects.toMatchObject({code: 'TIMEOUT'})
  const reader = f.manager.stream(token, 0, token.size - 1).getReader()
  void reader.closed.catch(() => {})
  await reader.read()
  for (let index = 0; index < 10; index++) {
   await vi.advanceTimersByTimeAsync(11999)
   await reader.read()
  }
  await vi.advanceTimersByTimeAsync(10)
  await expect(reader.read()).rejects.toMatchObject({code: 'TIMEOUT'})
  const next = mediaResponse(f.manager, new Request(result.tokenURL, {headers: {range: 'bytes=2-9'}}))
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
    await mediaResponse(manager, new Request(result.tokenURL, {headers: {range: 'bytes=1-9'}})).arrayBuffer()
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
