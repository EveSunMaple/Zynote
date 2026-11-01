import {mkdtemp, mkdir, writeFile, realpath, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createHash} from 'node:crypto'
import {afterEach, expect, it, vi} from 'vitest'
import {ManagedPlayback, type PhysicalMediaCall} from '@main/media/managed-playback'
import {mediaResponse} from '@main/media/media-protocol'

const taskID = '9e326025-30b4-45d0-bd27-4a30275adccb'
const summaryTaskID = '9e326025-30b4-45d0-bd27-4a30275adcca'
const requestID = '9e326025-30b4-45d0-bd27-4a30275adcc1'
const boundInput = {requestID, summaryTaskID, itemIndex: 0, expectedSourceBindingSHA256: 'b'.repeat(64)}
const releaseInput = {videoTaskID: taskID, expectedRefKind: 'receipt', expectedRefSHA256: 'c'.repeat(64)}
const roots: string[] = [],
 managers: ManagedPlayback[] = []
function gate<T>() {
 let resolve!: (value: T) => void, reject!: (error: unknown) => void
 const promise = new Promise<T>((yes, no) => {
  resolve = yes
  reject = no
 })
 return {promise, resolve, reject}
}
const flush = () => new Promise<void>(done => setImmediate(done))
afterEach(async () => {
 for (const manager of managers.splice(0)) await manager.dispose()
 for (const root of roots.splice(0)) await rm(root, {recursive: true, force: true, maxRetries: 5, retryDelay: 20})
})
async function fixture() {
 const root = await realpath(await mkdtemp(join(tmpdir(), 'zynote-bound-media-')))
 roots.push(root)
 await mkdir(join(root, 'VideoTasks', taskID), {recursive: true})
 const bytes = Buffer.alloc(2 * 1024 * 1024 + 19, 37),
  sha = createHash('sha256').update(bytes).digest('hex')
 await writeFile(join(root, 'VideoTasks', taskID, 'video.mp4'), bytes)
 const declaration = {
  version: 1,
  verification: 'declared',
  taskID,
  sourceKind: 'local',
  sourceIdentity: 'sha256:' + sha,
  sourceSHA256: sha,
  size: bytes.length,
  bindingRevision: 'd'.repeat(64),
  sourceBindingSHA256: 'b'.repeat(64),
  bindingAuthoritySHA256: 'e'.repeat(64)
 }
 const tracked = vi.fn((action: string, input: Record<string, unknown>): PhysicalMediaCall => {
  if (action.includes('playback-release'))
   return {
    result: Promise.resolve({
     plan: {
      version: 1,
      id: taskID,
      refKind: 'receipt',
      refSHA256: 'c'.repeat(64),
      releaseID: input.releaseID,
      state: action.includes('prepare') ? 'prepared' : 'released',
      files: []
     },
     tombstoneSHA256: 'f'.repeat(64)
    }),
    physicalSettled: Promise.resolve()
   }
  return {result: Promise.resolve(declaration), physicalSettled: Promise.resolve()}
 })
 const manager = new ManagedPlayback(
  root,
  async () => {
   throw new Error('A fallback forbidden')
  },
  undefined,
  tracked
 )
 managers.push(manager)
 const open = () => manager.openBound(boundInput)
 return {manager, tracked, declaration, bytes, open}
}
it('keeps A strict and rejects caller-supplied bound authority or paths before dispatch', async () => {
 const f = await fixture()
 await expect(f.manager.open({...boundInput, videoTaskID: taskID})).rejects.toBeDefined()
 await expect(
  f.manager.openBound({...boundInput, expectedBindingAuthoritySHA256: 'a'.repeat(64)})
 ).rejects.toBeDefined()
 await expect(f.manager.openBound({...boundInput, itemIndex: 1})).rejects.toBeDefined()
 expect(f.tracked).not.toHaveBeenCalled()
})
it('double-opens the exact bound declaration, checks HEAD/GET and every emitted block, and skips invalid ranges', async () => {
 const f = await fixture(),
  opened = await f.open()
 expect(f.tracked.mock.calls.map(([action]) => action)).toEqual([
  'video-ppt:bound-playback-source',
  'video-ppt:bound-playback-source'
 ])
 f.tracked.mockClear()
 expect((await mediaResponse(f.manager, new Request(opened.tokenURL, {headers: {range: 'bytes=7-2'}}))).status).toBe(
  416
 )
 expect((await mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'POST'}))).status).toBe(405)
 expect(f.tracked).not.toHaveBeenCalled()
 expect((await mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'HEAD'}))).body).toBeNull()
 const response = await mediaResponse(f.manager, new Request(opened.tokenURL))
 expect(Buffer.from(await response.arrayBuffer())).toEqual(f.bytes)
 expect(f.tracked).toHaveBeenCalledTimes(5)
 expect(f.tracked.mock.calls.every(([action]) => action === 'video-ppt:bound-playback-check')).toBe(true)
 expect(f.tracked.mock.calls[0][1]).toEqual({
  summaryTaskID,
  itemIndex: 0,
  expectedSourceBindingSHA256: 'b'.repeat(64),
  expectedBindingAuthoritySHA256: 'e'.repeat(64)
 })
}, 15000)
it('rejects a changed second declaration before minting any token', async () => {
 const f = await fixture()
 f.tracked.mockImplementationOnce(() => ({result: Promise.resolve(f.declaration), physicalSettled: Promise.resolve()}))
 f.tracked.mockImplementationOnce(() => ({
  result: Promise.resolve({...f.declaration, bindingAuthoritySHA256: '9'.repeat(64)}),
  physicalSettled: Promise.resolve()
 }))
 await expect(f.open()).rejects.toMatchObject({code: 'CONFLICT'})
})
it('stops a changed second block before enqueue while keeping only previously checked bytes', async () => {
 const f = await fixture(),
  opened = await f.open(),
  response = await mediaResponse(f.manager, new Request(opened.tokenURL)),
  reader = response.body!.getReader()
 const first = await reader.read()
 expect(first.value).toEqual(f.bytes.subarray(0, 1024 * 1024))
 f.tracked.mockImplementationOnce(() => ({
  result: Promise.resolve({...f.declaration, bindingRevision: '9'.repeat(64)}),
  physicalSettled: Promise.resolve()
 }))
 await expect(reader.read()).rejects.toBeDefined()
 expect((await mediaResponse(f.manager, new Request(opened.tokenURL))).status).toBe(404)
})
it('rejects an unknown-task late mint after even a rejected release cleared its fence', async () => {
 const f = await fixture(),
  late = gate<unknown>(),
  physical = gate<void>()
 f.tracked.mockImplementationOnce(() => ({result: late.promise, physicalSettled: physical.promise}))
 const opening = f.open().catch(error => error)
 f.tracked.mockImplementationOnce(() => ({
  result: Promise.resolve({status: 'rejected', mutation: 'none', code: 'CONFLICT', message: 'controlled rejection'}),
  physicalSettled: Promise.resolve()
 }))
 await expect(f.manager.release(releaseInput)).rejects.toMatchObject({code: 'CONFLICT'})
 late.resolve(f.declaration)
 physical.resolve()
 expect(await opening).toMatchObject({code: 'CANCELLED'})
 expect(f.tracked).toHaveBeenCalledTimes(2)
})
it('holds two HEAD physical slots through logical timeout and blocks release until their actual terminals', async () => {
 const f = await fixture(),
  opened = await f.open(),
  token = f.manager.get(opened.tokenURL.split('/').at(-1)!)!
 const a = gate<unknown>(),
  b = gate<unknown>(),
  pa = gate<void>(),
  pb = gate<void>()
 f.tracked.mockImplementationOnce(() => ({result: a.promise, physicalSettled: pa.promise}))
 f.tracked.mockImplementationOnce(() => ({result: b.promise, physicalSettled: pb.promise}))
 const heads = [
  mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'HEAD'})),
  mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'HEAD'}))
 ]
 a.reject(Object.assign(new Error('logical timeout'), {code: 'TIMEOUT'}))
 b.reject(Object.assign(new Error('logical timeout'), {code: 'TIMEOUT'}))
 expect((await Promise.all(heads)).map(r => r.status)).toEqual([404, 404])
 expect(token.streams.size).toBe(2)
 expect(token.reads.size).toBe(2)
 expect((await mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'HEAD'}))).status).toBe(429)
 let released = false
 const release = f.manager.release(releaseInput).then(() => {
  released = true
 })
 await flush()
 expect(released).toBe(false)
 expect(f.tracked.mock.calls.some(([action]) => action === 'video-ppt:finalize-playback-release')).toBe(false)
 pa.resolve()
 await flush()
 expect(released).toBe(false)
 pb.resolve()
 await release
 expect(released).toBe(true)
 expect(token.streams.size).toBe(0)
 expect(token.reads.size).toBe(0)
})

it('rechecks cancellation after a pending block check and before enqueue, draining release first', async () => {
 const f = await fixture(),
  opened = await f.open(),
  response = await mediaResponse(f.manager, new Request(opened.tokenURL)),
  reader = response.body!.getReader()
 const result = gate<unknown>(),
  physical = gate<void>()
 f.tracked.mockImplementationOnce(() => ({result: result.promise, physicalSettled: physical.promise}))
 const reading = reader.read().catch(error => error)
 // Preflight already checked; wait until this physical block RPC was dispatched.
 await expect.poll(() => f.tracked.mock.calls.length).toBe(4)
 let released = false
 const release = f.manager.release(releaseInput).then(() => {
  released = true
 })
 expect(await reading).toBeDefined()
 expect(released).toBe(false)
 result.resolve(f.declaration)
 physical.resolve()
 await release
 expect(released).toBe(true)
})
it('shares the four physical HEAD/GET slots across owners, including HEAD without a body', async () => {
 const fixtures = [await fixture(), await fixture(), await fixture()],
  opened = []
 for (const f of fixtures) opened.push(await f.open())
 const pending: {result: ReturnType<typeof gate<unknown>>; physical: ReturnType<typeof gate<void>>}[] = []
 const responses: Promise<Response>[] = []
 for (let owner = 0; owner < 2; owner++)
  for (let n = 0; n < 2; n++) {
   const result = gate<unknown>(),
    physical = gate<void>()
   pending.push({result, physical})
   fixtures[owner].tracked.mockImplementationOnce(() => ({result: result.promise, physicalSettled: physical.promise}))
   responses.push(mediaResponse(fixtures[owner].manager, new Request(opened[owner].tokenURL, {method: 'HEAD'})))
  }
 expect((await mediaResponse(fixtures[2].manager, new Request(opened[2].tokenURL, {method: 'HEAD'}))).status).toBe(429)
 for (let i = 0; i < pending.length; i++) {
  pending[i].result.resolve(fixtures[Math.floor(i / 2)].declaration)
  pending[i].physical.resolve()
 }
 expect((await Promise.all(responses)).map(r => r.status)).toEqual([200, 200, 200, 200])
})

it('lets a successful HEAD finish independently while another timed-out HEAD still owns its physical slot', async () => {
 const f = await fixture(),
  opened = await f.open(),
  token = f.manager.get(opened.tokenURL.split('/').at(-1)!)!
 const a = gate<unknown>(),
  b = gate<unknown>(),
  pa = gate<void>(),
  pb = gate<void>()
 f.tracked.mockImplementationOnce(() => ({result: a.promise, physicalSettled: pa.promise}))
 f.tracked.mockImplementationOnce(() => ({result: b.promise, physicalSettled: pb.promise}))
 let firstStatus = 0
 const first = mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'HEAD'})).then(response => {
  firstStatus = response.status
  return response
 })
 const second = mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'HEAD'}))
 b.reject(Object.assign(new Error('controlled logical timeout'), {code: 'TIMEOUT'}))
 expect((await second).status).toBe(404)
 a.resolve(f.declaration)
 pa.resolve()
 try {
  await expect.poll(() => firstStatus).toBe(200)
  expect(token.streams.size).toBe(1)
  expect(token.reads.size).toBe(1)
  let released = false
  const release = f.manager.release(releaseInput).then(() => {
   released = true
  })
  await flush()
  expect(released).toBe(false)
  expect(f.tracked.mock.calls.some(([action]) => action === 'video-ppt:finalize-playback-release')).toBe(false)
  pb.resolve()
  await release
  expect(released).toBe(true)
 } finally {
  pb.resolve()
  await first
 }
})

it('returns a second same-task mint independently of an older timed-out HEAD while release still drains that HEAD', async () => {
 const f = await fixture(),
  opened = await f.open(),
  token = f.manager.get(opened.tokenURL.split('/').at(-1)!)!
 const result = gate<unknown>(),
  physical = gate<void>()
 f.tracked.mockImplementationOnce(() => ({result: result.promise, physicalSettled: physical.promise}))
 const head = mediaResponse(f.manager, new Request(opened.tokenURL, {method: 'HEAD'}))
 result.reject(Object.assign(new Error('controlled logical timeout'), {code: 'TIMEOUT'}))
 expect((await head).status).toBe(404)
 let minted = false
 const second = f.manager.openBound({...boundInput, requestID: '9e326025-30b4-45d0-bd27-4a30275adcc2'}).then(value => {
  minted = true
  return value
 })
 try {
  await expect.poll(() => minted).toBe(true)
  const newToken = await second
  expect(newToken.tokenURL).not.toBe(opened.tokenURL)
  expect(token.streams.size).toBe(1)
  expect(token.reads.size).toBe(1)
  let released = false
  const release = f.manager.release(releaseInput).then(() => {
   released = true
  })
  await flush()
  expect(released).toBe(false)
  expect(f.tracked.mock.calls.some(([action]) => action === 'video-ppt:finalize-playback-release')).toBe(false)
  physical.resolve()
  await release
  expect(released).toBe(true)
 } finally {
  physical.resolve()
  await second
 }
})
