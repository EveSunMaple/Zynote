import {app} from 'electron'
import {spawn, type ChildProcess} from 'node:child_process'
import {existsSync} from 'node:fs'
import {join} from 'node:path'
import {z} from 'zod'
import type {EventPayload} from '@shared/types'
import {shellServices} from '@main/backend/shell-services'

const HELLO_TIMEOUT_MS = 8000
const CALL_TIMEOUT_MS = 10000
const MAX_RESTARTS = 3
const STDERR_TAIL_LIMIT = 8000

export type GoCoreStatus = 'starting' | 'ready' | 'restarting' | 'failed' | 'disposed'

type Pending = {
 resolve: (value: unknown) => void
 reject: (error: Error) => void
 timer: NodeJS.Timeout
}

export type GoCoreOptions = {
 appVersion: string
 dataRoot: string
 libraryRoot: string
 resourcesRoot: string
 promptsRoot: string
 portable: boolean
 onEvent: (event: EventPayload) => void
 onFailed: (reason: string) => void
}

export function coreBinaryPath() {
 return app.isPackaged
  ? join(process.resourcesPath, 'bin', 'zynote-core')
  : join(app.getAppPath(), 'backend', 'bin', 'zynote-core')
}

export class GoCore {
 private child: ChildProcess | undefined
 private stdoutTail = ''
 private stderrTail = ''
 private status: GoCoreStatus = 'starting'
 private pending = new Map<string, Pending>()
 private nextID = 0
 private restarts = 0
 private disposed = false
 private buffer = ''
 private options: GoCoreOptions
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
  this.child = spawn(binary, [], {
   stdio: ['pipe', 'pipe', 'pipe'],
   windowsHide: true,
   detached: process.platform !== 'win32'
  })
  this.buffer = ''
  this.child.stdout!.on('data', chunk => this.consume(String(chunk)))
  this.child.stderr!.on('data', chunk => {
   this.stderrTail = (this.stderrTail + String(chunk)).slice(-STDERR_TAIL_LIMIT)
  })
  this.child.on('exit', (code, signal) => this.handleExit(code, signal))
  this.child.on('error', error => this.handleExit(undefined, undefined, String(error)))
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
    libraryRoot: this.options.libraryRoot,
    resourcesRoot: this.options.resourcesRoot,
    promptsRoot: this.options.promptsRoot,
    portable: this.options.portable
   }
  })
  const timer = setTimeout(() => this.handleExit(undefined, undefined, '握手超时'), HELLO_TIMEOUT_MS)
  timer.unref?.()
  this.helloTimer = timer
 }

 async call(action: string, params: Record<string, unknown>): Promise<unknown> {
  if (this.status !== 'ready' || !this.child) throw new Error('Go 后端不可用')
  const id = 'c-' + ++this.nextID
  return await new Promise<unknown>((resolve, reject) => {
   const timer = setTimeout(() => {
    this.pending.delete(id)
    reject(new Error('Go 后端请求超时'))
   }, CALL_TIMEOUT_MS)
   this.pending.set(id, {resolve, reject, timer})
   this.send({jsonrpc: '2.0', id, method: action, params})
  })
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
  if (!child || child.exitCode !== null || child.signalCode !== null) return
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

 private consume(chunk: string) {
  this.buffer += chunk
  for (let index = this.buffer.indexOf('\n'); index >= 0; index = this.buffer.indexOf('\n')) {
   const line = this.buffer.slice(0, index)
   this.buffer = this.buffer.slice(index + 1)
   if (line.trim()) this.handleLine(line)
  }
  if (this.buffer.length > 1_000_000) this.buffer = ''
 }

 private handleLine(line: string) {
  this.stdoutTail = (this.stdoutTail + line).slice(-STDERR_TAIL_LIMIT)
  let frame: Record<string, unknown>
  try {
   frame = JSON.parse(line) as Record<string, unknown>
  } catch {
   return
  }
  const id = typeof frame.id === 'string' ? frame.id : undefined
  const method = typeof frame.method === 'string' ? frame.method : undefined
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
    item.reject(new Error(error.message || 'Go 后端请求失败'))
   } else item.resolve(frame.result)
  }
 }

 private async handleReverse(id: string, method: string, params: unknown) {
  const service = shellServices[method]
  if (!service) {
   this.send({jsonrpc: '2.0', id, error: {code: 'INPUT_INVALID', message: '未知壳服务: ' + method}})
   return
  }
  try {
   const object = z.record(z.string(), z.unknown()).parse(params || {})
   const result = await service(object)
   this.send({jsonrpc: '2.0', id, result: result ?? {}})
  } catch (error) {
   this.send({
    jsonrpc: '2.0',
    id,
    error: {code: 'INPUT_INVALID', message: error instanceof Error ? error.message : '壳服务失败'}
   })
  }
 }

 private handleExit(code: number | null | undefined, signal: string | null | undefined, reason?: string) {
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
   this.options.onFailed('Go 后端反复崩溃，已切换回 TS 后端: ' + detail)
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

 private send(frame: Record<string, unknown>) {
  try {
   this.child?.stdin?.write(JSON.stringify(frame) + '\n')
  } catch {}
 }
}
