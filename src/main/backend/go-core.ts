import {app} from 'electron'
import {spawn, type ChildProcess} from 'node:child_process'
import {existsSync} from 'node:fs'
import {join} from 'node:path'
import {StringDecoder} from 'node:string_decoder'
import {z} from 'zod'
import type {EventPayload} from '@shared/types'
import type {ShellService} from '@main/backend/shell-service'
import {backendFailure} from '@shared/ipc-result'

const HELLO_TIMEOUT_MS = 8000
const CALL_TIMEOUT_MS = 10000
const SLOW_CALL_TIMEOUT_MS = 90 * 1000
const LONG_CALL_TIMEOUT_MS = 30 * 60 * 1000
const MAX_RESTARTS = 3
const STDERR_TAIL_LIMIT = 8000
const MAX_FRAME_BYTES = 8 * 1024 * 1024
// These actions wait for real media/transcription work.  A 10s IPC timeout
// used to reject them while the Go backend kept running, which made the task
// center report a failure even though the download completed.
const LONG_RUNNING_ACTIONS = new Set([
 'video-ppt:prepare-playback-release',
 'video-ppt:finalize-playback-release',
 'chat',
 'settings:probe',
 'settings:test',
 'knowledge:ask',
 // 导入时先弹出文件选择框（用户可能停留很久），再逐个解析 PDF/Word。
 'knowledge:import',
 'knowledge:organize',
 'knowledge:rebuild',
 'knowledge:vector-rebuild',
 'knowledge:classification-suggest',
 'notes:generate',
 'practice:generate',
 'practice:answer',
 'practice:prepare',
 'questions:split',
 'study:parse',
 'subtitle-derived:start',
 'subtitle-derived:resume',
 'subtitle-sync:attach',
 'subtitle-sync:mixed',
 'subtitle-sync:resume',
 'subtitle-sync:start',
 'subtitles:generate',
 'textbook:manual',
 'textbook:apply',
 'textbook:clear',
 'textbook:analyze',
 'textbook:analyze-images',
 'textbook:pick', // Includes time spent choosing the file, followed by native PDF inspection.
 'textbook:ppt-ocr',
 'video-ppt:auto',
 'video-ppt:batch',
 'video-ppt:batch-subtitles',
 'video-ppt:export',
 'video-ppt:extract',
 'video-ppt:notes-export',
 'video-ppt:resume-subtitle',
 'video-ppt:series',
 'video-ppt:source',
 'video-ppt:subtitle',
 'video-ppt:subtitle-transcribe',
 'video-ppt:subtitle-transcribe-local',
 'video-ppt:summary',
 'video-ppt:summary-from-retained',
 'visual:prepare',
 'visual:prepare-resume',
 'visual:recover-output',
 'visual:recognize',
 'ppt-extract:analyze',
 'ppt-extract:export'
])
const SLOW_ACTIONS = new Set([
 'zhiyun:search',
 'zhiyun:lessons',
 'settings:models',
 'subtitle-derived:audit-publication',
 'video-ppt:playback-source'
])
// 单行回包上限：超出时丢弃这一行，并让对应请求明确报错，而不是一直等到超时。
const MAX_LINE_CHARS = 16_000_000
// 研究模型请求要等整节课字幕，单次常需 60–90 秒，两步工作流更久；其他 tools:request 动作保持 90 秒。
const LONG_TOOL_ACTIONS = new Set(['agent-create', 'workflow-action'])
function callTimeout(action: string, params?: Record<string, unknown>) {
 if (action === 'tools:request') {
  const inner = typeof params?.action === 'string' ? params.action : ''
  return LONG_TOOL_ACTIONS.has(inner) ? LONG_CALL_TIMEOUT_MS : SLOW_CALL_TIMEOUT_MS
 }
 if (action === 'video-ppt:bound-playback-source' || action === 'video-ppt:bound-playback-check') return 6000
 if (LONG_RUNNING_ACTIONS.has(action)) return LONG_CALL_TIMEOUT_MS
 if (SLOW_ACTIONS.has(action)) return SLOW_CALL_TIMEOUT_MS
 return CALL_TIMEOUT_MS
}

export type GoCoreStatus = 'starting' | 'ready' | 'restarting' | 'failed' | 'disposed'

type Pending = {
 tracked: boolean
 resolve: (value: unknown) => void
 reject: (error: Error) => void
 timer: NodeJS.Timeout
}

export type TrackedGoCall = {requestID: string; result: Promise<unknown>; physicalSettled: Promise<void>}

function isTerminalFrame(value: unknown): value is Record<string, unknown> & {id: string} {
 if (!value || typeof value !== 'object' || Array.isArray(value)) return false
 const frame = value as Record<string, unknown>
 if (
  frame.jsonrpc !== '2.0' ||
  Object.hasOwn(frame, 'method') ||
  typeof frame.id !== 'string' ||
  Object.hasOwn(frame, 'result') === Object.hasOwn(frame, 'error')
 )
  return false
 if (Object.hasOwn(frame, 'error')) {
  const error = frame.error as Record<string, unknown>
  if (
   !error ||
   typeof error !== 'object' ||
   Array.isArray(error) ||
   typeof error.code !== 'string' ||
   error.code.length === 0 ||
   typeof error.message !== 'string'
  )
   return false
 }
 return true
}

export type GoCoreOptions = {
 appVersion: string
 dataRoot: string
 configRoot: string
 appRoot: string
 libraryRoot: string
 previousLibraryRoot: string
 resourcesRoot: string
 promptsRoot: string
 nativePath: string
 portable: boolean
 services: Record<string, ShellService>
 onEvent: (event: EventPayload) => void
 onFailed: (reason: string) => void
 onUnavailable?: () => void
}

export function nativeBinaryPath() {
 const name = process.platform === 'win32' ? 'zynote-native.exe' : 'zynote-native'
 return app.isPackaged
  ? join(process.resourcesPath, 'bin', name)
  : join(app.getAppPath(), 'native', 'target', 'release', name)
}

export function coreBinaryPath() {
 const name = process.platform === 'win32' ? 'zynote-core.exe' : 'zynote-core'
 return app.isPackaged ? join(process.resourcesPath, 'bin', name) : join(app.getAppPath(), 'backend', 'bin', name)
}

export class GoCore {
 private child: ChildProcess | undefined
 private stdoutTail = ''
 private stderrTail = ''
 private status: GoCoreStatus = 'starting'
 private readonly pending = new Map<string, Pending>()
 private readonly physical = new Map<ChildProcess, Map<string, () => void>>()
 private readonly physicalBuffers = new Map<ChildProcess, string>()
 private nextID = 0
 private restarts = 0
 private disposed = false
 private buffer = ''
 private dropping = false
 private readonly stdoutDecoder = new StringDecoder('utf8')
 private readonly stderrDecoder = new StringDecoder('utf8')
 private readonly options: GoCoreOptions
 private helloTimer: NodeJS.Timeout | undefined

 constructor(options: GoCoreOptions) {
  this.options = options
 }

 get currentStatus() {
  return this.status
 }

 start() {
  const binary = coreBinaryPath()
  if (!existsSync(binary)) throw new Error('Go 后端二进制缺失，请先运行 pnpm backend:build')
  this.status = 'starting'
  const child = spawn(binary, [], {
   stdio: ['pipe', 'pipe', 'pipe'],
   windowsHide: true,
   detached: process.platform !== 'win32'
  })
  this.child = child
  this.buffer = ''
  this.dropping = false
  let spawned = false
  child.once('spawn', () => {
   spawned = true
  })
  child.stdout!.on('data', chunk => {
   const text = this.stdoutDecoder.write(chunk)
   this.observePhysical(child, text)
   if (this.child === child) this.consume(text)
  })
  // A closed helper pipe emits an asynchronous 'error', even if write() returned normally.
  child.stdin!.on('error', () => {
   if (this.child === child) child.kill()
  })
  this.child.stderr!.on('data', chunk => {
   this.stderrTail = (this.stderrTail + this.stderrDecoder.write(chunk)).slice(-STDERR_TAIL_LIMIT)
  })
  child.on('exit', (code, signal) => {
   this.settlePhysicalChild(child)
   if (this.child === child) this.handleExit(code, signal)
  })
  child.once('close', () => this.settlePhysicalChild(child))
  child.on('error', error => {
   // Failed spawn with no PID cannot have executed a request. Errors on a
   // spawned child, logical dispose and handshake timeout are not termination.
   if (!spawned && child.pid === undefined) this.settlePhysicalChild(child)
   if (this.child === child) this.handleExit(undefined, undefined, String(error))
  })
  this.send({
   jsonrpc: '2.0',
   id: 'c-hello',
   method: 'core.hello',
   params: {
    protocol: 1,
    appVersion: this.options.appVersion,
    electronVersion: process.versions.electron,
    platform: process.platform,
    dataRoot: this.options.dataRoot,
    configRoot: this.options.configRoot,
    appRoot: this.options.appRoot,
    libraryRoot: this.options.libraryRoot,
    previousLibraryRoot: this.options.previousLibraryRoot,
    resourcesRoot: this.options.resourcesRoot,
    promptsRoot: this.options.promptsRoot,
    nativePath: this.options.nativePath,
    portable: this.options.portable,
    packaged: app.isPackaged
   }
  })
  const timer = setTimeout(() => this.handleExit(undefined, undefined, '握手超时'), HELLO_TIMEOUT_MS)
  timer.unref?.()
  this.helloTimer = timer
 }

 async call(action: string, params: Record<string, unknown>): Promise<unknown> {
  return await this.beginCall(action, params, false).result
 }

 callTracked(action: string, params: Record<string, unknown>): TrackedGoCall {
  return this.beginCall(action, params, true)
 }

 private beginCall(action: string, params: Record<string, unknown>, tracked: boolean): TrackedGoCall {
  if (this.status !== 'ready' || !this.child) throw new Error('Go 后端不可用')
  const child = this.child
  const id = 'c-' + ++this.nextID
  let physicalSettled = Promise.resolve()
  const result = new Promise<unknown>((resolve, reject) => {
   const timer = setTimeout(
    () => {
     this.pending.delete(id)
     reject(backendFailure({code: 'TIMEOUT', message: 'Go 后端请求超时'}))
    },
    callTimeout(action, params)
   )
   this.pending.set(id, {resolve, reject, timer, tracked})
   const error = this.send({jsonrpc: '2.0', id, method: action, params})
   if (error) {
    clearTimeout(timer)
    this.pending.delete(id)
    reject(error)
    return
   }
   // A request rejected before writing to the pipe has no outstanding backend work.
   if (tracked) {
    physicalSettled = new Promise<void>(settle => {
     let requests = this.physical.get(child)
     if (!requests) {
      requests = new Map()
      this.physical.set(child, requests)
     }
     requests.set(id, settle)
    })
   }
  })
  return {requestID: id, result, physicalSettled}
 }

 private settlePhysicalChild(child: ChildProcess) {
  for (const settle of this.physical.get(child)?.values() || []) settle()
  this.physical.delete(child)
  this.physicalBuffers.delete(child)
 }

 private observePhysical(child: ChildProcess, chunk: string) {
  if (!this.physical.has(child)) return
  let buffer = (this.physicalBuffers.get(child) || '') + chunk
  for (let index = buffer.indexOf('\n'); index >= 0; index = buffer.indexOf('\n')) {
   const line = buffer.slice(0, index)
   buffer = buffer.slice(index + 1)
   try {
    const frame: unknown = JSON.parse(line)
    if (!isTerminalFrame(frame)) continue
    const requests = this.physical.get(child)
    requests?.get(frame.id)?.()
    requests?.delete(frame.id)
    if (requests?.size === 0) this.physical.delete(child)
   } catch {}
  }
  // Malformed/oversized transport data cannot confirm termination; keep its
  // request fenced until an actual child exit/close if no terminal is parsed.
  if (this.physical.has(child)) this.physicalBuffers.set(child, buffer.length > 1_000_000 ? '' : buffer)
  else this.physicalBuffers.delete(child)
 }

 dispose() {
  this.disposed = true
  for (const [id, item] of this.pending) {
   clearTimeout(item.timer)
   item.reject(new Error('Go 后端已退出'))
   this.pending.delete(id)
  }
  const child = this.child
  this.child = undefined
  if (child?.exitCode !== null || child?.signalCode !== null) return
  try {
   if (process.platform !== 'win32' && child.pid) {
    try {
     process.kill(-child.pid, 'SIGTERM')
    } catch {
     child.kill('SIGTERM')
    }
   } else child.kill('SIGTERM')
  } catch {}
  const killer = setTimeout(() => {
   try {
    if (process.platform !== 'win32' && child.pid) process.kill(-child.pid, 'SIGKILL')
    else child.kill('SIGKILL')
   } catch {}
  }, 3000)
  killer.unref?.()
 }

 // biome-ignore format: NOSONAR must stay on the function line
 private consume(chunk: string) { // NOSONAR: transitional TS fallback/UI flow retained for behavior compatibility; decomposition tracked separately to avoid sequencing regressions.
  this.buffer += chunk
  for (let index = this.buffer.indexOf('\n'); index >= 0; index = this.buffer.indexOf('\n')) {
   const line = this.buffer.slice(0, index)
   this.buffer = this.buffer.slice(index + 1)
   if (this.dropping) this.dropping = false
   else if (line.trim()) this.handleLine(line)
  }
  if (this.buffer.length > MAX_LINE_CHARS) this.dropOversizedLine()
 }

 // 回包以 {"jsonrpc":"2.0","id":"c-N" 开头，超长时取出 id 让该请求立即失败，余下部分丢到下一个换行为止。
 private dropOversizedLine() {
  const id = /^\{"jsonrpc":"2\.0","id":"(c-\d+)"/.exec(this.buffer)?.[1]
  this.buffer = ''
  this.dropping = true
  const item = id ? this.pending.get(id) : undefined
  if (!id || !item) return
  this.pending.delete(id)
  clearTimeout(item.timer)
  item.reject(new Error('Go 后端响应过大，已丢弃；请缩小本次请求的范围'))
 }

 // biome-ignore format: NOSONAR must stay on the function line
 private handleLine(line: string) { // NOSONAR: cohesive flow; further extraction would risk behavior changes.
  // NOSONAR: cohesive flow; further extraction would risk behavior changes.
  // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
  this.stdoutTail = (this.stdoutTail + line).slice(-STDERR_TAIL_LIMIT)
  let frame: Record<string, unknown>
  try {
   frame = JSON.parse(line) as Record<string, unknown>
  } catch {
   return
  }
  if (!frame || typeof frame !== 'object' || Array.isArray(frame)) return
  const id = typeof frame.id === 'string' ? frame.id : undefined
  const method = typeof frame.method === 'string' ? frame.method : undefined
  if (id !== undefined && this.pending.get(id)?.tracked && !isTerminalFrame(frame)) return
  if (method && id !== undefined) {
   void this.handleReverse(id, method, frame.params).catch(() => {})
   return
  }
  if (method && id === undefined) {
   if (method === 'event') this.options.onEvent(frame.params as EventPayload)
   return
  }
  if (!method && id !== undefined) {
   if (id === 'c-hello') {
    if (this.helloTimer) clearTimeout(this.helloTimer)
    this.helloTimer = undefined
    if (frame.error) {
     const error = frame.error as {code?: string; message?: string}
     this.status = 'failed'
     this.options.onFailed('Go 后端握手被拒绝: ' + (error.message || ''))
     return
    }
    this.status = 'ready'
    this.restarts = 0
    return
   }
   const item = this.pending.get(id)
   if (!item) return
   this.pending.delete(id)
   clearTimeout(item.timer)
   if (frame.error) {
    const error = frame.error as {code?: string; message?: string}
    item.reject(backendFailure(error))
   } else item.resolve(frame.result)
  }
 }

 private async handleReverse(id: string, method: string, params: unknown) {
  const service = this.options.services[method]
  if (!service) {
   this.send({jsonrpc: '2.0', id, error: {code: 'INPUT_INVALID', message: '未知壳服务: ' + method}})
   return
  }
  try {
   const object = z.record(z.string(), z.unknown()).parse(params || {})
   const result = await service(object)
   const error = this.send({jsonrpc: '2.0', id, result: result ?? {}})
   if (error) throw error
  } catch (error) {
   this.send({
    jsonrpc: '2.0',
    id,
    error: {code: 'INPUT_INVALID', message: error instanceof Error ? error.message : '壳服务失败'}
   })
  }
 }

 private handleExit(code: number | null | undefined, signal: string | null | undefined, reason?: string) {
  this.options.onUnavailable?.()
  const wasReady = this.status === 'ready'
  this.child = undefined
  this.status = wasReady ? 'restarting' : 'failed'
  for (const [id, item] of this.pending) {
   clearTimeout(item.timer)
   item.reject(new Error('Go 后端已退出'))
   this.pending.delete(id)
  }
  const detail = reason || `code=${code ?? 'null'} signal=${signal ?? 'null'}`
  if (this.disposed) return
  if (!wasReady) {
   this.options.onFailed('Go 后端启动失败: ' + detail + ' stderr: ' + this.stderrTail.slice(-500))
   return
  }
  this.restarts++
  if (this.restarts > MAX_RESTARTS) {
   this.status = 'failed'
   this.options.onFailed('Go 后端反复崩溃，已停止；请重启应用后重试: ' + detail)
   return
  }
  const delay = 1000 * this.restarts
  setTimeout(() => {
   if (!this.disposed && this.status === 'restarting') {
    try {
     this.start()
    } catch (error) {
     this.status = 'failed'
     this.options.onFailed(error instanceof Error ? error.message : 'Go 后端重启失败')
    }
   }
  }, delay)
 }

 private send(frame: Record<string, unknown>): Error | undefined {
  try {
   const line = JSON.stringify(frame) + '\n'
   if (Buffer.byteLength(line) > MAX_FRAME_BYTES) return new Error('请求数据超过 8 MiB，请减少本次发送的数据')
   if (!this.child?.stdin?.writable) return new Error('Go 后端连接已关闭')
   this.child.stdin.write(line)
  } catch {
   return new Error('发送 Go 后端请求失败')
  }
 }
}
