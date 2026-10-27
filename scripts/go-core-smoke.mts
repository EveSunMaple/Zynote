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
// biome-ignore format: NOSONAR must stay on the function line
child.stdout.on('data', chunk => { // NOSONAR: build/fixture script; bounded sequential verification steps intentionally kept together.
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
type CourseResult = {id: string; name: string; created_at: string}

const hello = (await call('core.hello', {
 protocol: 1,
 appVersion: '0.0.1',
 electronVersion: 'smoke',
 platform: 'darwin',
 dataRoot,
 libraryRoot: dataRoot,
 resourcesRoot,
 promptsRoot: projectRoot,
 nativePath: join(projectRoot, 'native', 'target', 'release', 'zynote-native'),
 portable: false
})) as HelloResult
check('hello protocol', hello.protocol === 1)
check('hello platform', hello.platform === 'darwin')
check('hello capabilities', hello.capabilities.length === 152)

const bootstrap = (await call('db:bootstrap')) as {ok: boolean}
check('bootstrap ok', bootstrap.ok === true)

const course = (await call('course:create', {name: '冒烟课程'})) as CourseResult
check('course create id', course.id.length === 36)
check('course create name', course.name === '冒烟课程')
check('course created_at format', course.created_at.length === 19)

const sessions = (await call('sessions', undefined)) as {title: string}[]
check('default session', sessions.length === 1 && sessions[0].title === '新对话')

const fs = await import('node:fs')
let dirExists = false
try {
 await fs.promises.access(join(dataRoot, 'Transcripts', '冒烟课程'))
 dirExists = true
} catch {}
check('library dirs english', dirExists)

const {PDFDocument: SmokePdfDoc, StandardFonts: SmokeFonts} = await import('pdf-lib')
const smokePdf = await SmokePdfDoc.create()
const smokeFont = await smokePdf.embedFont(SmokeFonts.Helvetica)
const smokePage = smokePdf.addPage([300, 200])
smokePage.drawText('bridge smoke with plenty of extractable content', {x: 40, y: 120, size: 14, font: smokeFont})
await writeFile(join(dataRoot, 'smoke.pdf'), await smokePdf.save())
const nativePath = join(projectRoot, 'native', 'target', 'release', 'zynote-native')
if (fs.existsSync(nativePath)) {
 try {
  const pdfResult = (await call('pdf.text', {path: join(dataRoot, 'smoke.pdf')})) as {
   numPages: number
   pages: {items: {text: string}[]}[]
  }
  check('bridge pdf.text numPages', pdfResult.numPages === 1)
  check(
   'bridge pdf.text content',
   pdfResult.pages[0].items.some(item => item.text.includes('bridge smoke'))
  )
 } catch (error) {
  console.error('FAIL bridge pdf.text', error)
  process.exitCode = 1
 }
 const plain = (await call('document:parse', {path: join(dataRoot, 'smoke.pdf')})) as {
  text: string
  needsOcr: boolean
 }
 check('document parse pdf', plain.text.includes('bridge smoke') && !plain.needsOcr)
} else console.log('skip bridge pdf.text (native binary missing)')

await writeFile(
 join(dataRoot, 'smoke.srt'),
 '1\n00:00:01,000 --> 00:00:03,000\n冒烟字幕第一句\n\n2\n00:00:03,000 --> 00:00:05,000\n第二句\n'
)
const transcript = (await call('transcript:read', {path: join(dataRoot, 'smoke.srt')})) as {
 estimated: boolean
 segments: {start: number; end: number; text: string}[]
}
check('transcript read srt', !transcript.estimated && transcript.segments.length === 2)

await writeFile(join(dataRoot, 'smoke-transcript.txt'), '1. 语音识别\n00:00:01 第一句\n00:00:04 第二句\n')
const parsedTranscript = (await call('transcript:read', {path: join(dataRoot, 'smoke-transcript.txt')})) as {
 estimated: boolean
 segments: {start: number; text: string}[]
}
check(
 'transcript read txt',
 parsedTranscript.estimated &&
  parsedTranscript.segments[0].start === 1000 &&
  parsedTranscript.segments[1].start === 4000
)

const mediaOptions = (await call('media:options')) as {defaults: {container: string}; heights: number[]}
check('media options defaults', mediaOptions.defaults.container === 'mkv')
check('media options heights', mediaOptions.heights.length === 10)
const mediaList = (await call('media:list')) as unknown[]
check('media list empty', mediaList.length === 0)
const authList = (await call('media:auth-list')) as unknown[]
check('media auth list empty', authList.length === 0)

await writeFile(join(dataRoot, 'smoke-note.md'), '课程笔记\n第二行')
const markdownParse = (await call('document:parse', {path: join(dataRoot, 'smoke-note.md')})) as {text: string}
check('document parse md', markdownParse.text.startsWith('课程笔记'))

const sourcePath = join(dataRoot, 'import-source.md')
await writeFile(sourcePath, '## 学长资料\n这是导入的内容段落')
const addedFiles = (await call('files:add', {
 courseId: course.id,
 kind: 'senior_notes',
 paths: [sourcePath]
})) as {id: string; path: string; needs_ocr: number}[]
check('files add count', addedFiles.length === 1)
const detailAfter = (await call('course:detail', {id: course.id})) as {files: {id: string; chunks: string[]}[]}
check('files add detail', detailAfter.files.length === 1 && detailAfter.files[0].chunks.length >= 1)
const removed = (await call('files:remove', {courseId: course.id, id: addedFiles[0].id})) as boolean
check('files remove', removed === true)
const detailClean = (await call('course:detail', {id: course.id})) as {files: unknown[]}
check('files remove detail', detailClean.files.length === 0)

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

const health = (await call('db:health')) as HealthResult & {ok: boolean; integrity: string}
check('health after bootstrap', health.exists === true && health.ok === true && health.integrity === 'ok')

const backup = (await call('db:backup', undefined)) as {path: string; bytes: number}
check('backup created', backup.bytes > 0 && backup.path.startsWith(dataRoot))

child.kill('SIGTERM')
setTimeout(() => process.exit(process.exitCode ?? 0), 200).unref?.()
