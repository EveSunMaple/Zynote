import {spawn} from 'node:child_process'
import {build} from 'esbuild'
import {createRequire} from 'node:module'
import {mkdir, mkdtemp, readFile, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath, pathToFileURL} from 'node:url'

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const work = await mkdtemp(join(tmpdir(), 'zynote-knowledge-'))
const root = join(work, '知识库数据')
const require = createRequire(import.meta.url)
const bundle = join(projectRoot, 'tmp', `knowledge-store-${process.pid}.mjs`)
await mkdir(dirname(bundle), {recursive: true})
await build({
 entryPoints: [join(projectRoot, 'src/main/knowledge/knowledge-store.ts')],
 bundle: true,
 platform: 'node',
 format: 'esm',
 packages: 'external',
 outfile: bundle
})
type KnowledgeStoreLike = {
 read(id: string): {text: string}
 search(query: string): {hits: {evidence_id: string; bvid: string; content: string}[]}
 citation(id: string): {bvid: string; content: string}
 import(input: Record<string, unknown>): {entry: {bvid: string}}
 close(): void
}
const {NativeKnowledgeStore} = (await import(pathToFileURL(bundle).href)) as {
 NativeKnowledgeStore: new (root: string) => KnowledgeStoreLike
}

const runGo = (env: Record<string, string>) =>
 new Promise<void>((resolvePromise, reject) => {
  const child = spawn('go', ['test', './internal/knowledge', '-run', 'TestKnowledgeCrossLanguage', '-count=1'], {
   cwd: join(projectRoot, 'backend'),
   env: {...process.env, ...env},
   stdio: 'inherit'
  })
  child.on('error', reject)
  child.on('exit', code => (code === 0 ? resolvePromise() : reject(new Error('Go 侧知识库测试失败：' + code))))
 })

const result = async () => JSON.parse(await readFile(join(work, 'go-result.json'), 'utf8'))

try {
 await runGo({ZYNOTE_KNOWLEDGE_DIR: work, ZYNOTE_KNOWLEDGE_MODE: 'save'})
 const goResult = await result()
 const entry = goResult.entry
 const store = new NativeKnowledgeStore(root)
 const read = store.read(entry.bvid)
 if (read.text !== '跨语言知识库校验：Go 写入的资料可以被 TypeScript 读取。')
  throw new Error('TypeScript 无法读取 Go 导入的知识库资料')
 const search = store.search('TypeScript')
 if (!search.hits.some(hit => hit.evidence_id === (goResult.evidence as string)))
  throw new Error('TypeScript 无法检索 Go 生成的证据')
 const citation = store.citation(goResult.evidence)
 if (citation.bvid !== entry.bvid || !citation.content.includes('TypeScript'))
  throw new Error('TypeScript 引用校验失败')
 store.close()

 const writer = new NativeKnowledgeStore(root)
 const text = '跨语言知识库校验：TypeScript 写入的资料可以被 Go 读取。'
 const imported = writer.import({
  title: 'TS 资料',
  text,
  locator: 'https://example.com/ts',
  sourceHash: require('node:crypto').createHash('sha256').update(text).digest('hex'),
  category: '跨语言'
 })
 writer.close()

 await runGo({
  ZYNOTE_KNOWLEDGE_DIR: work,
  ZYNOTE_KNOWLEDGE_MODE: 'read',
  ZYNOTE_KNOWLEDGE_ID: imported.entry.bvid
 })
 const goRead = await result()
 if (goRead.text !== text || !goRead.citation?.evidence_id || !goRead.search?.hits.length)
  throw new Error('Go 无法读取 TypeScript 生成的知识库资料')
 console.log('ok knowledge roundtrip', entry.bvid.slice(0, 12), imported.entry.bvid.slice(0, 12))
} finally {
 await rm(bundle, {force: true})
 await rm(work, {recursive: true, force: true})
}
