import {spawn} from 'node:child_process'
import {mkdtemp, writeFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'

const projectRoot = fileURLToPath(new URL('..', import.meta.url))
const dataRoot = await mkdtemp(join(tmpdir(), 'zynote-smoke-'))
const resourcesRoot = join(projectRoot, 'resources')

await writeFile(
 join(dataRoot, 'settings.json'),
 JSON.stringify({
  theme: 'dark',
  browserWidth: 480,
  browserOpen: false,
  maxMinutes: 30,
  maxGB: 2.5,
  compress: true,
  prompts: {},
  qwenProfile: {
   id: 'qwen',
   name: '测试配置',
   baseUrl: 'https://api.example.com/v1',
   model: 'qwen-max',
   visionModel: '',
   provider: 'qwen',
   apiFormat: 'chat_completions',
   models: ['qwen-max']
  }
 })
)
await writeFile(
 join(dataRoot, 'identity.json'),
 JSON.stringify({userName: '连酱', assistantName: '梅酱', userAvatar: '', assistantAvatar: '', persona: ''})
)

const child = spawn(join(projectRoot, 'backend', 'bin', 'zynote-core'), [], {stdio: ['pipe', 'pipe', 'pipe']})
child.stderr.on('data', chunk => console.error('[core-stderr]', String(chunk).trim()))
const buffer: string[] = []
let pendingIndex = 0
const pending = new Map()
child.stdout.on('data', chunk => {
 buffer.push(String(chunk))
 let joined = buffer.join('')
 for (;;) {
  const index = joined.indexOf('\n')
  if (index < 0) break
  const line = joined.slice(0, index)
  joined = joined.slice(index + 1)
  const frame = JSON.parse(line)
  const id = typeof frame.id === 'string' ? frame.id : undefined
  if (frame.method && id) continue
  if (frame.method) continue
  if (id && pending.has(id)) {
   const {resolve, reject, timer} = pending.get(id)
   clearTimeout(timer)
   pending.delete(id)
   frame.error ? reject(new Error(frame.error.code + ': ' + frame.error.message)) : resolve(frame.result)
  }
 }
 buffer.length = 0
 if (joined) buffer.push(joined)
})

let sequence = 0
function call(method: string, params?: Record<string, unknown>) {
 const id = 'c-' + ++sequence
 return new Promise((resolve, reject) => {
  const timer = setTimeout(() => {
   pending.delete(id)
   reject(new Error(method + ' timeout'))
  }, 8000)
  timer.unref?.()
  pending.set(id, {resolve, reject, timer})
  child.stdin.write(JSON.stringify({jsonrpc: '2.0', id, method, ...(params ? {params} : {})}) + '\n')
 })
}

function check(name: string, condition: boolean) {
 if (!condition) {
  console.error('FAIL', name)
  process.exitCode = 1
 } else console.log('ok', name)
}

type HelloResult = {protocol: number; platform: string; capabilities: string[]}
type InfoResult = {version: string; electron: string; platform: string}
type SettingsResult = {
 theme: string
 connection: string
 activeProfile: string
 codex?: unknown
 profiles: {hasKey: boolean; name: string; keyLength: number}[]
}
type IdentityResult = {assistantName: string}
type AppearanceResult = {global: {fontSize: number}}
type HealthResult = {exists: boolean}

const hello = (await call('core.hello', {
 protocol: 1,
 appVersion: '0.0.1',
 electronVersion: 'smoke',
 platform: 'darwin',
 dataRoot,
 libraryRoot: dataRoot,
 resourcesRoot,
 portable: false
})) as HelloResult
check('hello protocol', hello.protocol === 1)
check('hello platform', hello.platform === 'darwin')
check('hello capabilities', hello.capabilities.length === 6)

const info = (await call('app:info')) as InfoResult
check('app:info version', info.version === '0.0.1')
check('app:info platform', info.platform === 'darwin')

const settings = (await call('settings:get')) as SettingsResult
check('settings theme', settings.theme === 'dark')
check('settings connection', settings.connection === 'qwen')
check('settings activeProfile', settings.activeProfile === 'qwen')
check('settings profile hasKey', settings.profiles[0].hasKey === false)
check('settings legacy hidden', settings.codex === undefined)

const identity = (await call('identity:get')) as IdentityResult
check('identity default name', identity.assistantName === 'Zynote')

const appearance = (await call('appearance:get')) as AppearanceResult
check('appearance default', appearance.global.fontSize === 16)

const health = (await call('db:health')) as HealthResult
check('health missing db', health.exists === false)

let backupFailed = false
try {
 await call('db:backup')
} catch {
 backupFailed = true
}
check('backup without db fails', backupFailed)

child.kill('SIGTERM')
setTimeout(() => process.exit(process.exitCode ?? 0), 200).unref?.()
