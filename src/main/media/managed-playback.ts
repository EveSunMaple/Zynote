import {createHash, randomBytes, randomUUID} from 'node:crypto'
import {open, lstat, realpath, type FileHandle} from 'node:fs/promises'
import {join, resolve, isAbsolute} from 'node:path'
import {
 playbackDeclarationSchema,
 boundPlaybackDeclarationSchema,
 boundPlaybackOpenSchema,
 playbackOpenSchema,
 playbackReleaseSchema,
 playbackReleaseResultSchema,
 playbackReleasePrepareSchema,
 type PlaybackDeclaration,
 type BoundPlaybackOpen,
 type BoundPlaybackDeclaration
} from '@shared/managed-playback'

const BLOCK = 1024 * 1024
const MAX_SIZE = 8 * 1024 ** 3
const TTL = 30 * 60 * 1000
const IDLE = 5 * 60 * 1000
const LIMIT = 32 * 1024 ** 3
let globalReservations = 0
let globalHashing = false
let globalStreams = 0
let releaseGeneration = 0
const playbackOwners = new Set<ManagedPlayback>()
const releaseFences = new Set<string>()
const releasesInFlight = new Set<string>()
function taskFence(root: string, id: string) {
 const key = join(managedRoot(root), 'VideoTasks', id)
 return process.platform === 'win32' ? key.toLowerCase() : key
}
export function playbackFailure(code: string, message: string) {
 return Object.assign(new Error(message), {code})
}
const unavailable = () => playbackFailure('UNAVAILABLE', '托管媒体不可用')
function digest(body: Uint8Array) {
 return createHash('sha256').update(body).digest('hex')
}
function check(signal: AbortSignal) {
 if (signal.aborted) throw signal.reason
}

function waitWithAbort<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
 return new Promise((resolve, reject) => {
  const abort = () => reject(signal.reason)
  signal.addEventListener('abort', abort, {once: true})
  work.then(
   value => {
    signal.removeEventListener('abort', abort)
    resolve(value)
   },
   error => {
    signal.removeEventListener('abort', abort)
    reject(error)
   }
  )
  if (signal.aborted) abort()
 })
}

export type PlaybackToken = {
 id: string
 requestID: string
 taskID: string
 file: FileHandle
 size: number
 hashes: string[]
 sourceSHA256: string
 bindingRevision: string
 created: number
 touched: number
 bytes: number
 reads: Set<Promise<void>>
 streams: Set<AbortController>
 revoked: boolean
 mime: string
 bound?: {input: BoundPlaybackOpen; declaration: BoundPlaybackDeclaration}
}

function sameManagedPath(first: string, second: string) {
 const a = resolve(first),
  b = resolve(second)
 return process.platform === 'win32' ? a.toLowerCase() === b.toLowerCase() : a === b
}
async function safeFile(path: string) {
 const info = await lstat(path)
 if (info.isSymbolicLink() || !info.isFile() || !sameManagedPath(await realpath(path), path)) throw unavailable()
 return info
}
function managedRoot(libraryRoot: string) {
 if (!isAbsolute(libraryRoot)) throw unavailable()
 const base = resolve(libraryRoot)
 if (process.platform === 'win32' && (base.startsWith('\\\\') || base.slice(2).includes(':'))) throw unavailable()
 return base
}
async function openManaged(libraryRoot: string, taskID: string) {
 const base = managedRoot(libraryRoot)
 for (const path of [base, join(base, 'VideoTasks'), join(base, 'VideoTasks', taskID)]) {
  const info = await lstat(path)
  if (info.isSymbolicLink() || !info.isDirectory() || !sameManagedPath(await realpath(path), path)) throw unavailable()
 }
 const path = join(base, 'VideoTasks', taskID, 'video.mp4')
 const before = await safeFile(path)
 const file = await open(path, 'r')
 // No await/validation after open here: the caller must own the handle before
 // any fallible post-open work so every failure enters its physical-close ledger.
 return {file, before, path}
}

async function readBlock(file: FileHandle, index: number, size: number, signal: AbortSignal) {
 check(signal)
 const position = index * BLOCK,
  length = Math.min(BLOCK, size - position),
  buffer = Buffer.alloc(length)
 let offset = 0
 while (offset < length) {
  check(signal)
  const {bytesRead} = await file.read(buffer, offset, length - offset, position + offset)
  check(signal)
  if (bytesRead === 0) throw playbackFailure('CONFLICT', '托管媒体字节已经改变')
  offset += bytesRead
 }
 check(signal)
 return buffer
}
function containerMime(block: Buffer) {
 if (block.length >= 12 && block.toString('ascii', 4, 8) === 'ftyp') return 'video/mp4'
 if (block.subarray(0, 4).equals(Buffer.from([0x1a, 0x45, 0xdf, 0xa3]))) return 'video/webm'
 return 'application/octet-stream'
}

type ClosingHandle = Pick<PlaybackToken, 'taskID' | 'file' | 'reads'>
export type PhysicalMediaCall = {result: Promise<unknown>; physicalSettled: Promise<void>}
export class ManagedPlayback {
 private readonly tokens = new Map<string, PlaybackToken>()
 private readonly pending = new Map<string, AbortController>()
 private readonly pendingTasks = new Map<string, string | undefined>()
 private readonly mintTasks = new Map<Promise<unknown>, string | undefined>()
 private readonly failedCloses = new Set<ClosingHandle>()
 private readonly closingTasks = new Map<Promise<void>, string>()
 private readonly requests = new Map<string, {fingerprint: string; tokenID?: string}>()
 private readonly closing = new Set<Promise<void>>()
 private readonly mints = new Set<Promise<unknown>>()
 private readonly physicalRPCs = new Map<Promise<void>, {taskID?: string; action: string}>()
 private ownerReservations = 0
 private disposed = false
 private readonly cleanupTimer: ReturnType<typeof setInterval>
 constructor(
  private readonly libraryRoot: string,
  private readonly declaration: (taskID: string, sha: string) => Promise<unknown>,
  private readonly releaseCall?: (action: string, input: Record<string, unknown>) => Promise<unknown>,
  private readonly trackedCall?: (action: string, input: Record<string, unknown>) => PhysicalMediaCall
 ) {
  playbackOwners.add(this)
  this.cleanupTimer = setInterval(() => this.expire(), 15000)
  this.cleanupTimer.unref?.()
 }
 // Main-only observer. An unresolved initial bound declaration has no task ID,
 // so it conservatively participates in every task's release drain.
 async observeRPC(taskID: string | undefined, call: PhysicalMediaCall, action = 'metadata') {
  this.physicalRPCs.set(call.physicalSettled, {taskID, action})
  void call.physicalSettled.then(() => {
   this.physicalRPCs.delete(call.physicalSettled)
   this.maybeForgetOwner()
  })
  const result = await call.result
  await call.physicalSettled
  return result
 }
 private maybeForgetOwner() {
  if (
   this.disposed &&
   this.failedCloses.size === 0 &&
   this.physicalRPCs.size === 0 &&
   this.mints.size === 0 &&
   this.closing.size === 0
  )
   playbackOwners.delete(this)
 }
 private physicalForTask(taskID: string | undefined, releaseOnly = false) {
  return [...this.physicalRPCs]
   .filter(
    ([, record]) =>
     (taskID === undefined || record.taskID === undefined || record.taskID === taskID) &&
     (!releaseOnly ||
      record.action === 'video-ppt:prepare-playback-release' ||
      record.action === 'video-ppt:finalize-playback-release')
   )
   .map(([work]) => work)
 }
 private async releaseRPC(action: string, input: Record<string, unknown>, taskID: string) {
  if (this.trackedCall) return await this.observeRPC(taskID, this.trackedCall(action, input), action)
  return await this.releaseCall!(action, input)
 }
 async open(raw: unknown) {
  return this.beginOpen(playbackOpenSchema.parse(raw))
 }
 async openBound(raw: unknown) {
  return this.beginOpen(boundPlaybackOpenSchema.parse(raw))
 }
 private async beginOpen(
  input: {requestID: string; videoTaskID: string; expectedSourceSHA256: string} | BoundPlaybackOpen
 ) {
  const taskID = 'videoTaskID' in input ? input.videoTaskID : undefined
  managedRoot(this.libraryRoot)
  if (this.disposed) throw unavailable()
  if (taskID && releaseFences.has(taskFence(this.libraryRoot, taskID))) throw playbackFailure('BUSY', '该视频正在释放')
  const fingerprint = JSON.stringify(input),
   existing = this.requests.get(input.requestID)
  if (existing) throw playbackFailure(existing.fingerprint === fingerprint ? 'BUSY' : 'CONFLICT', '媒体请求编号已使用')
  // Reserve before metadata RPC or full-file hashing: pending mints count too.
  if (globalHashing || globalReservations >= 4 || this.ownerReservations >= 2)
   throw playbackFailure('BUSY', '另一项媒体正在核验，请稍后重试')
  globalReservations++
  this.ownerReservations++
  globalHashing = true
  const controller = new AbortController()
  this.pending.set(input.requestID, controller)
  this.pendingTasks.set(input.requestID, taskID)
  this.requests.set(input.requestID, {fingerprint})
  const deadline = setTimeout(() => controller.abort(playbackFailure('TIMEOUT', '媒体核验超时')), 120000)
  const work = this.mint(input, controller, fingerprint, deadline, releaseGeneration)
  this.mints.add(work)
  this.mintTasks.set(work, taskID)
  void work
   .finally(() => {
    this.mints.delete(work)
    this.mintTasks.delete(work)
    this.maybeForgetOwner()
   })
   .catch(() => {})
  return await waitWithAbort(work, controller.signal)
 }
 private async mint(
  input: {requestID: string; videoTaskID: string; expectedSourceSHA256: string} | BoundPlaybackOpen,
  controller: AbortController,
  fingerprint: string,
  deadline: ReturnType<typeof setTimeout>,
  generation: number
 ) {
  let file: FileHandle | undefined,
   registered = false,
   taskID = 'videoTaskID' in input ? input.videoTaskID : undefined
  const mintPhysical = new Set<Promise<void>>()
  const retainPhysical = (work: Promise<void>) => {
   mintPhysical.add(work)
   void work.then(() => mintPhysical.delete(work))
  }
  try {
   const boundInput = 'summaryTaskID' in input ? input : undefined
   const declared = boundInput
    ? await this.readBoundDeclaration(boundInput, undefined, retainPhysical)
    : await this.readDeclaration(
       taskID!,
       'expectedSourceSHA256' in input ? input.expectedSourceSHA256 : '',
       retainPhysical
      )
   taskID = declared.taskID
   this.pendingTasks.set(input.requestID, taskID)
   if (boundInput && generation !== releaseGeneration)
    throw playbackFailure('CANCELLED', '媒体核验期间发生释放，请重新打开')
   check(controller.signal)
   if (releaseFences.has(taskFence(this.libraryRoot, taskID))) throw playbackFailure('BUSY', '该视频正在释放')
   const opened = await openManaged(this.libraryRoot, taskID)
   file = opened.file
   const openedInfo = await file.stat(),
    afterOpen = await safeFile(opened.path)
   if (
    openedInfo.dev !== opened.before.dev ||
    openedInfo.ino !== opened.before.ino ||
    openedInfo.dev !== afterOpen.dev ||
    openedInfo.ino !== afterOpen.ino ||
    !openedInfo.isFile()
   )
    throw unavailable()
   if (openedInfo.size !== declared.size || openedInfo.size <= 0 || openedInfo.size > MAX_SIZE)
    throw playbackFailure('CONFLICT', '媒体长度与任务绑定不一致')
   const hash = createHash('sha256'),
    hashes: string[] = []
   let mime = 'application/octet-stream'
   for (let index = 0; index * BLOCK < declared.size; index++) {
    const block = await readBlock(file, index, declared.size, controller.signal)
    hash.update(block)
    hashes.push(digest(block))
    if (index === 0) mime = containerMime(block)
   }
   if (hash.digest('hex') !== declared.sourceSHA256 || (await file.stat()).size !== declared.size)
    throw playbackFailure('CONFLICT', '媒体字节与任务绑定不一致')
   const after = boundInput
    ? await this.readBoundDeclaration(boundInput, taskID, retainPhysical)
    : await this.readDeclaration(
       taskID,
       'expectedSourceSHA256' in input ? input.expectedSourceSHA256 : '',
       retainPhysical
      )
   if (JSON.stringify(after) !== JSON.stringify(declared)) throw playbackFailure('CONFLICT', '视频来源记录已经改变')
   check(controller.signal)
   if ((boundInput && generation !== releaseGeneration) || releaseFences.has(taskFence(this.libraryRoot, taskID)))
    throw playbackFailure('BUSY', '该视频正在释放')
   const id = randomBytes(32).toString('base64url'),
    now = Date.now()
   const token: PlaybackToken = {
    id,
    requestID: input.requestID,
    taskID,
    file,
    size: declared.size,
    hashes,
    sourceSHA256: declared.sourceSHA256,
    bindingRevision: declared.bindingRevision,
    created: now,
    touched: now,
    bytes: 0,
    reads: new Set(),
    streams: new Set(),
    revoked: false,
    mime,
    bound: boundInput ? {input: boundInput, declaration: declared as BoundPlaybackDeclaration} : undefined
   }
   this.tokens.set(id, token)
   this.requests.set(input.requestID, {fingerprint, tokenID: id})
   registered = true
   return {
    requestID: input.requestID,
    tokenURL: 'lecture-media://play/' + id,
    sourceSHA256: token.sourceSHA256,
    size: token.size,
    expiresAt: new Date(now + TTL).toISOString(),
    bindingRevision: token.bindingRevision
   }
  } catch (error) {
   const code = (error as {code?: string})?.code
   const failure =
    code && ['UNAVAILABLE', 'CONFLICT', 'INPUT_INVALID', 'BUSY', 'CANCELLED', 'TIMEOUT'].includes(code)
     ? error
     : unavailable()
   if (mintPhysical.size) controller.abort(failure)
   throw failure
  } finally {
   clearTimeout(deadline)
   await Promise.allSettled([...mintPhysical])
   this.pending.delete(input.requestID)
   this.pendingTasks.delete(input.requestID)
   globalHashing = false
   if (!registered) {
    this.requests.delete(input.requestID)
    if (file) await this.closeHandle({taskID: taskID!, file, reads: new Set()})
    else {
     globalReservations--
     this.ownerReservations--
    }
   }
  }
 }
 private async readBoundDeclaration(
  input: BoundPlaybackOpen,
  taskID: string | undefined,
  retainPhysical: (work: Promise<void>) => void
 ) {
  if (!this.trackedCall) throw unavailable()
  const {requestID: _requestID, ...reference} = input
  const call = this.trackedCall('video-ppt:bound-playback-source', reference)
  retainPhysical(call.physicalSettled)
  const value = boundPlaybackDeclarationSchema.parse(
   await this.observeRPC(taskID, call, 'video-ppt:bound-playback-source')
  )
  if (value.sourceBindingSHA256 !== input.expectedSourceBindingSHA256 || (taskID && value.taskID !== taskID))
   throw playbackFailure('CONFLICT', '摘要播放绑定已经改变')
  return value
 }
 private async readDeclaration(
  taskID: string,
  sha: string,
  retainPhysical: (work: Promise<void>) => void
 ): Promise<PlaybackDeclaration> {
  let raw: unknown
  if (this.trackedCall) {
   const call = this.trackedCall('video-ppt:playback-source', {id: taskID, expectedSourceSHA256: sha})
   retainPhysical(call.physicalSettled)
   raw = await this.observeRPC(taskID, call, 'video-ppt:playback-source')
  } else raw = await this.declaration(taskID, sha)
  const value = playbackDeclarationSchema.parse(raw)
  if (value.taskID !== taskID || value.sourceSHA256 !== sha) throw playbackFailure('CONFLICT', '视频来源记录已经改变')
  return value
 }
 get(id: string, touch = true) {
  const token = this.tokens.get(id)
  if (!token || token.revoked) return undefined
  if (Date.now() - token.created >= TTL || Date.now() - token.touched >= IDLE) {
   this.revoke(id)
   return undefined
  }
  if (touch) token.touched = Date.now()
  return token
 }
 revoke(id: string) {
  const token = this.tokens.get(id)
  if (!token) return
  this.tokens.delete(id)
  this.requests.delete(token.requestID)
  token.revoked = true
  for (const stream of token.streams) stream.abort(playbackFailure('CANCELLED', '媒体已关闭'))
  void this.closeHandle(token).catch(() => {})
 }
 private closeHandle(held: ClosingHandle) {
  this.failedCloses.delete(held)
  const closing = Promise.allSettled([...held.reads])
   .then(() => held.file.close())
   .then(() => {
    globalReservations--
    this.ownerReservations--
   })
   .catch(error => {
    this.failedCloses.add(held)
    throw error
   })
   .finally(() => {
    this.closing.delete(closing)
    this.closingTasks.delete(closing)
    this.maybeForgetOwner()
   })
  this.closing.add(closing)
  this.closingTasks.set(closing, held.taskID)
  void closing.catch(() => {})
  return closing
 }

 closeAll() {
  for (const pending of this.pending.values()) pending.abort(playbackFailure('CANCELLED', '媒体核验已取消'))
  for (const id of this.tokens.keys()) this.revoke(id)
 }
 async dispose() {
  this.disposed = true
  clearInterval(this.cleanupTimer)
  for (const held of this.failedCloses) void this.closeHandle(held).catch(() => {})
  this.closeAll()
  await Promise.allSettled([...this.mints, ...this.closing, ...this.physicalRPCs.keys()])
  await Promise.allSettled([...this.closing])
  this.maybeForgetOwner()
 }
 private async quiesceTask(taskID: string) {
  for (const held of [...this.failedCloses]) if (held.taskID === taskID) void this.closeHandle(held).catch(() => {})
  for (const [requestID, id] of this.pendingTasks)
   if (id === undefined || id === taskID)
    this.pending.get(requestID)?.abort(playbackFailure('CANCELLED', '媒体正在释放'))
  for (const [id, token] of this.tokens) if (token.taskID === taskID) this.revoke(id)
  await Promise.allSettled(
   [...this.mintTasks].filter(([, id]) => id === undefined || id === taskID).map(([work]) => work)
  )
  await Promise.all([...this.closingTasks].filter(([, id]) => id === taskID).map(([work]) => work))
  await Promise.allSettled(this.physicalForTask(taskID))
  if ([...this.failedCloses].some(held => held.taskID === taskID)) throw unavailable()
  this.maybeForgetOwner()
 }
 async release(raw: unknown) {
  const input = playbackReleaseSchema.parse(raw)
  const fence = taskFence(this.libraryRoot, input.videoTaskID)
  if (this.disposed || (!this.releaseCall && !this.trackedCall)) throw unavailable()
  // This happens synchronously before the first await, across all owners.
  if (releasesInFlight.has(fence)) throw playbackFailure('BUSY', '该视频正在释放')
  if (!releaseFences.has(fence) && releaseFences.size >= 64) throw playbackFailure('BUSY', '待对账的视频释放请求过多')
  const previouslyUncertain = releaseFences.has(fence)
  releasesInFlight.add(fence)
  releaseFences.add(fence)
  releaseGeneration++
  try {
   const candidate = {
    id: input.videoTaskID,
    expectedRefKind: input.expectedRefKind,
    expectedRefSHA256: input.expectedRefSHA256,
    releaseID: randomUUID()
   }
   const prepared = playbackReleasePrepareSchema.parse(
    await this.releaseRPC('video-ppt:prepare-playback-release', candidate, input.videoTaskID)
   )
   if ('status' in prepared) {
    if (!previouslyUncertain) releaseFences.delete(fence)
    throw playbackFailure(prepared.code, prepared.message)
   }
   const plan = prepared.plan
   if (
    plan.id !== candidate.id ||
    plan.refKind !== candidate.expectedRefKind ||
    plan.refSHA256 !== candidate.expectedRefSHA256
   )
    throw playbackFailure('CONFLICT', '视频释放计划不一致')
   const owners = [...playbackOwners].filter(owner => taskFence(owner.libraryRoot, input.videoTaskID) === fence)
   await Promise.all(owners.map(owner => owner.quiesceTask(input.videoTaskID)))
   const finalized = playbackReleaseResultSchema.parse(
    await this.releaseRPC(
     'video-ppt:finalize-playback-release',
     {
      ...candidate,
      releaseID: plan.releaseID,
      expectedTombstoneSHA256: prepared.tombstoneSHA256
     },
     input.videoTaskID
    )
   )
   const result = finalized.plan
   if (
    result.id !== plan.id ||
    result.refKind !== plan.refKind ||
    result.refSHA256 !== plan.refSHA256 ||
    result.releaseID !== plan.releaseID ||
    result.state !== 'released' ||
    JSON.stringify(result.files) !== JSON.stringify(plan.files)
   )
    throw playbackFailure('CONFLICT', '视频释放结果不一致')
   releaseFences.delete(fence)
   return {videoTaskID: result.id, state: result.state}
  } finally {
   const pendingRelease = this.physicalForTask(input.videoTaskID, true)
   if (pendingRelease.length) void Promise.allSettled(pendingRelease).then(() => releasesInFlight.delete(fence))
   else releasesInFlight.delete(fence)
  }
 }
 private expire() {
  for (const id of this.tokens.keys()) this.get(id, false)
 }
 close(requestID: string) {
  this.pending.get(requestID)?.abort(playbackFailure('CANCELLED', '媒体核验已取消'))
  const tokenID = this.requests.get(requestID)?.tokenID
  if (tokenID) this.revoke(tokenID)
 }
 private liveToken(token: PlaybackToken, signal: AbortSignal) {
  check(signal)
  const now = Date.now()
  if (
   token.revoked ||
   now - token.created >= TTL ||
   now - token.touched >= IDLE ||
   releaseFences.has(taskFence(this.libraryRoot, token.taskID))
  ) {
   this.revoke(token.id)
   throw unavailable()
  }
 }
 private async checkBound(token: PlaybackToken, signal: AbortSignal, retainPhysical: (work: Promise<void>) => void) {
  this.liveToken(token, signal)
  if (!token.bound) return
  if (!this.trackedCall) throw unavailable()
  const {input, declaration} = token.bound
  const call = this.trackedCall('video-ppt:bound-playback-check', {
   summaryTaskID: input.summaryTaskID,
   itemIndex: input.itemIndex,
   expectedSourceBindingSHA256: declaration.sourceBindingSHA256,
   expectedBindingAuthoritySHA256: declaration.bindingAuthoritySHA256
  })
  retainPhysical(call.physicalSettled)
  const checked = boundPlaybackDeclarationSchema.parse(
   await this.observeRPC(token.taskID, call, 'video-ppt:bound-playback-check')
  )
  if (JSON.stringify(checked) !== JSON.stringify(declaration)) throw playbackFailure('CONFLICT', '摘要播放绑定已经改变')
  this.liveToken(token, signal)
 }
 async preflight(token: PlaybackToken, signal: AbortSignal) {
  if (!token.bound) return
  if (token.revoked || globalStreams >= 4 || token.streams.size >= 2) throw playbackFailure('BUSY', '媒体读取繁忙')
  const controller = new AbortController()
  const abort = () => controller.abort(playbackFailure('CANCELLED', '媒体读取已取消'))
  signal.addEventListener('abort', abort, {once: true})
  if (signal.aborted) abort()
  token.streams.add(controller)
  globalStreams++
  let settle!: () => void
  const physical = new Promise<void>(resolve => {
   settle = resolve
  })
  token.reads.add(physical)
  let rpcPhysical: Promise<void> | undefined
  const observed = this.checkBound(token, controller.signal, work => {
   rpcPhysical = work
  })
  const work = observed.finally(async () => {
   if (rpcPhysical) await Promise.allSettled([rpcPhysical])
   signal.removeEventListener('abort', abort)
   token.streams.delete(controller)
   globalStreams--
   token.reads.delete(physical)
   settle()
  })
  // Caller cancellation or logical timeout can return early; the physical
  // cleanup still owns its slot and read until the actual RPC terminal.
  void work.catch(() => {})
  try {
   await waitWithAbort(observed, controller.signal)
   await work
  } catch (error) {
   if (!['CANCELLED', 'TIMEOUT'].includes((error as {code?: string})?.code ?? '')) this.revoke(token.id)
   throw error
  }
 }
 stream(token: PlaybackToken, start: number, end: number, signal?: AbortSignal) {
  if (Date.now() - token.created >= TTL || Date.now() - token.touched >= IDLE) this.revoke(token.id)
  if (token.revoked || globalStreams >= 4 || token.streams.size >= 2) throw playbackFailure('BUSY', '媒体读取繁忙')
  if (token.bytes + end - start + 1 + 2 * BLOCK > LIMIT) {
   this.revoke(token.id)
   throw unavailable()
  }
  if (signal?.aborted) throw playbackFailure('CANCELLED', '媒体读取已取消')
  const controller = new AbortController()
  const abortRequest = () => controller.abort(playbackFailure('CANCELLED', '媒体读取已取消'))
  signal?.addEventListener('abort', abortRequest, {once: true})
  token.streams.add(controller)
  globalStreams++
  let position = start,
   released = false,
   inFlight = false
  let idle: ReturnType<typeof setTimeout>
  let output: ReadableStreamDefaultController<Uint8Array> | undefined
  const release = () => {
   if (released) return
   released = true
   clearTimeout(idle)
   clearTimeout(deadline)
   signal?.removeEventListener('abort', abortRequest)
   token.streams.delete(controller)
   globalStreams--
  }
  const resetIdle = () => {
   clearTimeout(idle)
   idle = setTimeout(() => controller.abort(playbackFailure('TIMEOUT', '媒体读取停滞')), 15000)
  }
  const deadline = setTimeout(() => controller.abort(playbackFailure('TIMEOUT', '媒体读取超时')), 120000)
  controller.signal.addEventListener(
   'abort',
   () => {
    try {
     output?.error(controller.signal.reason)
    } catch {}
    if (!inFlight) release()
   },
   {once: true}
  )
  return new ReadableStream<Uint8Array>(
   {
    start: target => {
     output = target
     resetIdle()
    },
    pull: async target => {
     if (released) return
     inFlight = true
     let settle!: () => void
     const physical = new Promise<void>(resolve => {
      settle = resolve
     })
     token.reads.add(physical)
     let rpcPhysical: Promise<void> | undefined
     try {
      check(controller.signal)
      if (Date.now() - token.created >= TTL) {
       this.revoke(token.id)
       check(controller.signal)
      }
      if (position > end) {
       target.close()
       release()
       return
      }
      const index = Math.floor(position / BLOCK),
       block = await readBlock(token.file, index, token.size, controller.signal)
      if (Date.now() - token.created >= TTL) {
       this.revoke(token.id)
       check(controller.signal)
      }
      if ((await token.file.stat()).size !== token.size || digest(block) !== token.hashes[index])
       throw playbackFailure('CONFLICT', '托管媒体字节已经改变')
      const offset = position - index * BLOCK,
       length = Math.min(block.length - offset, end - position + 1)
      const now = Date.now()
      if (now - token.created >= TTL || now - token.touched >= IDLE) {
       this.revoke(token.id)
       check(controller.signal)
      }
      token.bytes += block.length
      token.touched = now
      if (token.bytes > LIMIT) throw unavailable()
      await this.checkBound(token, controller.signal, work => {
       rpcPhysical = work
      })
      this.liveToken(token, controller.signal)
      target.enqueue(block.subarray(offset, offset + length))
      position += length
      resetIdle()
     } catch (error) {
      try {
       target.error(error)
      } catch {}
      controller.abort(error)
      const code = (error as {code?: string})?.code
      if (code !== 'CANCELLED' && code !== 'TIMEOUT') this.revoke(token.id)
     } finally {
      if (rpcPhysical) await Promise.allSettled([rpcPhysical])
      inFlight = false
      token.reads.delete(physical)
      settle()
      if (controller.signal.aborted) release()
     }
    },
    cancel: () => {
     controller.abort(playbackFailure('CANCELLED', '媒体读取已取消'))
     if (!inFlight) release()
    }
   },
   {highWaterMark: 0}
  )
 }
}
