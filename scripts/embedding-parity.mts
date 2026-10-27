import {spawn} from 'node:child_process'
import {createInterface} from 'node:readline'
import {build} from 'esbuild'
import {mkdir, mkdtemp, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath, pathToFileURL} from 'node:url'

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const work = await mkdtemp(join(tmpdir(), 'zynote-embedding-'))
const bundle = join(projectRoot, 'tmp', `knowledge-embedding-${process.pid}.mjs`)
await mkdir(dirname(bundle), {recursive: true})
await build({
 entryPoints: [join(projectRoot, 'src/main/knowledge/knowledge-embedding.ts')],
 bundle: true,
 platform: 'node',
 format: 'esm',
 packages: 'external',
 outfile: bundle
})
const {KnowledgeEmbedding} = (await import(pathToFileURL(bundle).href)) as {
 KnowledgeEmbedding: new (
  directory: string
 ) => {
  encode(text: string, query?: boolean): Promise<number[]>
  dispose(): Promise<void>
 }
}
const model = join(projectRoot, 'resources/knowledge/onnx-bge')
const helper = join(projectRoot, 'native/target/debug/zynote-native')
const child = spawn(helper, ['--mode', 'embed', '--embed-model', model], {stdio: ['pipe', 'pipe', 'pipe']})
child.stderr.setEncoding('utf8')
child.stderr.on('data', chunk => console.error('[embed-stderr]', String(chunk).trim()))
child.on('exit', (code, signal) => console.error('[embed-exit]', code, signal))
const lines: string[] = []
const reader = createInterface({input: child.stdout})
reader.on('line', line => lines.push(line))
let sequence = 0
const call = (method: string, params: Record<string, unknown>) =>
 new Promise<Record<string, unknown>>((resolvePromise, reject) => {
  const id = 'e-' + ++sequence
  const timer = setTimeout(() => reject(new Error(method + ' timeout; lines=' + lines.length)), 120000)
  const listener = () => {
   const index = lines.findIndex(line => {
    try {
     return JSON.parse(line).id === id
    } catch {
     return false
    }
   })
   if (index < 0) return
   const frame = JSON.parse(lines[index])
   lines.splice(index, 1)
   clearTimeout(timer)
   reader.off('line', listener)
   frame.error ? reject(new Error(frame.error.message)) : resolvePromise(frame.result)
  }
  reader.on('line', listener)
  child.stdin.write(JSON.stringify({jsonrpc: '2.0', id, method, params}) + '\n')
  listener()
 })
const cosine = (left: number[], right: number[]) => {
 let dot = 0,
  leftLength = 0,
  rightLength = 0
 for (let index = 0; index < Math.min(left.length, right.length); index++) {
  dot += left[index] * right[index]
  leftLength += left[index] ** 2
  rightLength += right[index] ** 2
 }
 return dot / Math.sqrt(leftLength * rightLength)
}
try {
 const embedding = new KnowledgeEmbedding(model)
 const samples = [
  ['机器学习课堂笔记', false],
  ['hello world semantic search', false],
  ['跨语言语义检索', true]
 ] as const
 let worst = 1
 for (const [text, query] of samples) {
  // biome-ignore format: NOSONAR must stay on the await line
  const expected = await embedding.encode(text, query) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  // biome-ignore format: NOSONAR must stay on the await line
  const result = (await call('embed.text', {text, query})) as {vector: number[]} // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  if (result.vector.length !== 512) throw new Error('Rust 向量维度不符')
  const similarity = cosine(expected, result.vector)
  if (!Number.isFinite(similarity)) throw new Error('相似度无效')
  worst = Math.min(worst, similarity)
  console.log('sample', text, 'cosine', similarity.toFixed(6))
 }
 await embedding.dispose()
 // q8 量化内核在不同 ONNX Runtime 版本间存在约 1e-2 的余弦差异（fp32 与 q8 本身即约 0.9926）。
 if (worst < 0.99) throw new Error('Rust 与 TypeScript 语义向量差异过大：' + worst)
 console.log('ok embedding parity', worst.toFixed(6))
} finally {
 child.stdin.end()
 child.kill()
 await rm(bundle, {force: true})
 await rm(work, {recursive: true, force: true})
}
