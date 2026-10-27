import {lstat, mkdir, readFile, realpath, writeFile} from 'node:fs/promises'
import {join, resolve} from 'node:path'
import {createHash} from 'node:crypto'
import {z} from 'zod'
import {NativeResearchStore} from '@main/research/research-store'
import {NativeKnowledgeStore} from '@main/knowledge/knowledge-store'
import {NativeResearchSources} from '@main/research/research-sources'
import {NativeResearchExtensions} from '@main/research/research-extensions'
import {NativeResearchEngine, type ResearchModel} from '@main/research/research-engine'
import {NativeResearchNotes} from '@main/research/research-notes'
import {NativeResearchWorkflows} from '@main/research/research-workflow'
import {NativeResearchMcp} from '@main/research/research-mcp'
import {NativeImService} from '@main/im/core/im-service'
import type {MediaCrypt} from '@main/security/worker-crypt'
const op = z.string().regex(/^[a-f0-9]{32}$/)
/** Staged native tools dispatcher. Unimplemented channels are errors, never empty success receipts. */
export class NativeResearchService {
 private readonly store: NativeResearchStore
 private readonly knowledge: NativeKnowledgeStore
 private readonly sources: NativeResearchSources
 private readonly extensions: NativeResearchExtensions
 private readonly engine: NativeResearchEngine
 private readonly notes: NativeResearchNotes
 private readonly workflows: NativeResearchWorkflows
 private readonly mcp: NativeResearchMcp
 private im: NativeImService | undefined
 private connection = ''
 private ready: Promise<unknown> | undefined
 private closed = false
 constructor(
  private readonly root: string,
  private readonly model: ResearchModel,
  private readonly resources?: string,
  private readonly crypt?: MediaCrypt
 ) {
  this.store = new NativeResearchStore(root)
  this.knowledge = new NativeKnowledgeStore(root)
  this.sources = new NativeResearchSources(this.knowledge, this.store.rootId)
  this.extensions = new NativeResearchExtensions(this.store)
  this.mcp = new NativeResearchMcp(this.store)
  this.engine = new NativeResearchEngine(
   this.store,
   this.sources,
   model,
   () => this.connection,
   this.extensions,
   this.mcp
  )
  this.notes = new NativeResearchNotes(this.store, this.engine, root)
  this.workflows = new NativeResearchWorkflows(this.store, this.sources, this.engine)
 }
 private async executeImConfigure(action: string, args: Record<string, unknown>) {
  if (action === 'im') {
   if (!this.resources) throw new Error('通讯组件资源路径缺失')
   this.im ??= new NativeImService(
    this.root,
    this.resources,
    undefined,
    this.model,
    {
     id: this.store.rootId,
     search: query => this.sources.search(query),
     verify: sources => this.sources.verify(sources)
    },
    this.crypt
   )
   return {
    handled: true,
    value: await this.im.execute(z.string().parse(args.action), args.value || {}, this.connection)
   }
  }
  if (action !== 'configure') return {handled: false}
  const value = z.object({connection: z.string().min(1).max(200), model: z.string().max(200)}).parse(args)
  if (this.connection && this.connection !== value.connection)
   for (const task of this.engine.list()) if (task.status === 'running') this.engine.stop(task.task_id)
  this.connection = value.connection
  this.im?.configure(value.connection)
  return {handled: true, value: true}
 }
 private executeMaterials(action: string, args: Record<string, unknown>) {
  if (action === 'materials')
   return {
    handled: true,
    value: this.sources.catalog(
     z
      .string()
      .max(200)
      .parse(args.query || ''),
     z
      .number()
      .int()
      .parse(args.offset || 0)
    )
   }
  if (action === 'conversations')
   return {
    handled: true,
    value: this.store.conversations(
     z
      .number()
      .int()
      .parse(args.offset || 0)
    )
   }
  if (action === 'conversation-create')
   return {handled: true, value: this.store.createConversation(z.string().parse(args.title))}
  if (action === 'conversation-read') return {handled: true, value: this.store.conversation(z.string().parse(args.id))}
  if (action === 'conversation-organization') {
   const value = this.store.conversation(z.string().parse(args.id))
   return {handled: true, value: {revision: value.revision, pinned: value.pinned, title: value.title}}
  }
  if (action === 'conversation-organize') return {handled: true, value: this.store.organizeConversation(args)}
  return {handled: false}
 }
 private executeMemoryExtensions(action: string, args: Record<string, unknown>) {
  if (action === 'memories') return {handled: true, value: this.store.memories()}
  if (action === 'memory-change')
   return {handled: true, value: this.store.changeMemory(args.value, args.deleting === true)}
  if (action === 'memory-accept') return {handled: true, value: this.engine.acceptMemory(op.parse(args.id), args.value)}
  if (action === 'skill-markdown')
   return {handled: true, value: this.extensions.parseMarkdown(z.string().parse(args.text))}
  if (action === 'extension-change') return {handled: true, value: this.extensions.change(args)}
  return {handled: false}
 }
 private async executeMcpAgent(action: string, args: Record<string, unknown>) {
  if (action === 'mcp-change') return {handled: true, value: this.mcp.change(args)}
  if (action === 'mcp-discover')
   return {handled: true, value: await this.mcp.discover(args, AbortSignal.timeout(120000))}
  if (action === 'agent-create') return {handled: true, value: await this.engine.create(args)} // NOSONAR: await retained to support the alternate async implementation path.
  if (action === 'agent-read') {
   const id = op.parse(args.id)
   return {
    handled: true,
    value: this.store.legacyResearchTasks().find(task => task.task_id === id) || this.engine.read(id)
   }
  }
  if (action === 'agent-stop') return {handled: true, value: await this.engine.stop(op.parse(args.id))} // NOSONAR: await retained to support the alternate async implementation path.
  if (action !== 'agent-export') return {handled: false}
  const task = this.engine.verifyResult(op.parse(args.id)),
   text =
    '# ' +
    task.question +
    '\n\n' +
    task.note +
    '\n\n## 范围与限制\n\n' +
    task.limitations +
    '\n\n## 来源\n\n' +
    task.sources
     .filter(source => source.read)
     .map(source => `### ${source.source_id} · ${source.title}\n${source.url}\n\n${source.text}`)
     .join('\n\n')
  return {handled: true, value: await this.exportFile('研究成果', task.task_id + '.md', text)}
 }
 private executeNotes(action: string, args: Record<string, unknown>) {
  if (action === 'note-preview') return {handled: true, value: this.notes.preview(op.parse(args.id))}
  if (action === 'note-save') return {handled: true, value: this.notes.save(op.parse(args.id), args.expected)}
  if (action === 'notes')
   return {
    handled: true,
    value: this.notes.list(
     z.string().parse(args.query || ''),
     z.string().parse(args.state || 'active'),
     z
      .number()
      .int()
      .parse(args.offset || 0)
    )
   }
  if (action === 'note-read') return {handled: true, value: this.notes.read(op.parse(args.id))}
  if (action === 'note-revise') return {handled: true, value: this.notes.revise(args.value)}
  if (action === 'note-lifecycle') return {handled: true, value: this.notes.lifecycle(args.value)}
  if (action === 'note-source') return {handled: true, value: this.notes.source(op.parse(args.id))}
  if (action === 'note-export') return {handled: true, value: this.notes.export(op.parse(args.id))}
  return {handled: false}
 }
 private async executeWorkflows(action: string, args: Record<string, unknown>) {
  if (action === 'workflow-create') return {handled: true, value: await this.workflows.create(args)} // NOSONAR: await retained to support the alternate async implementation path.
  if (action === 'workflow-action') {
   if (this.store.legacyWorkflows().some(flow => flow.workflow_id === args.id))
    throw new Error('旧工作流需要重新选择当前资料建立新任务，不会重放旧请求')
   return {handled: true, value: await this.workflows.action(args)} // NOSONAR: await retained to support the alternate async implementation path.
  }
  if (action !== 'workflow-export') return {handled: false}
  const id = op.parse(args.id),
   old = this.store.legacyWorkflows().find(flow => flow.workflow_id === id)
  if (old)
   return {
    handled: true,
    value: await this.exportFile(
     '研究成果',
     old.workflow_id + '-工作流.md',
     '# ' +
      old.question +
      '\n\n> 历史工作流导出（未重新核验）\n> ' +
      old.notice +
      '\n\n' +
      old.steps
       .map(
        step =>
         '## ' +
         step.name +
         '\n\n保存状态：' +
         step.status +
         '；正文哈希：' +
         (step.hash_verified ? '与保存值一致' : '未确认') +
         '\n\n' +
         step.answer
       )
       .join('\n\n')
    )
   }
  const flow = this.workflows.verify(id),
   text =
    '# ' +
    flow.question +
    '\n\n' +
    flow.steps.map(step => '## ' + step.name + '\n\n' + step.answer).join('\n\n') +
    '\n\n## 来源版本\n\n```json\n' +
    JSON.stringify(flow.bindings, null, 2) +
    '\n```\n'
  return {handled: true, value: await this.exportFile('研究成果', flow.workflow_id + '-工作流.md', text)}
 }
 private executeOverview(action: string) {
  if (action !== 'overview') return {handled: false}
  return {
   handled: true,
   value: {
    root: this.store.rootId,
    extensions: this.extensions.list(),
    mcp: this.mcp.list(),
    tasks: [...this.engine.list(), ...this.store.legacyResearchTasks()],
    workflows: [...this.workflows.list(), ...this.store.legacyWorkflows()],
    migration: {
     runtimeLanguage: 'typescript',
     legacyRuntimeRequired: false,
     externalAcceptance: '详见版本说明；本地模拟不代表所有真实账号和站点已验收'
    },
    runtime: {
     dependencies: [{id: 'typescript', label: '原生 TypeScript 研究后台', state: 'detected', version: 'native-v1'}]
    }
   }
  }
 }
 async execute(action: string, input: unknown = {}): Promise<unknown> {
  if (this.closed) throw new Error('研究后台已关闭')
  this.ready ??= this.knowledge.importLegacyBaseline().then(() => this.store.baselineLegacy())
  await this.ready
  const args = z.record(z.string(), z.unknown()).parse(input)
  for (const group of [
   (name: string, value: Record<string, unknown>) => this.executeImConfigure(name, value),
   (name: string, value: Record<string, unknown>) => this.executeMaterials(name, value),
   (name: string, value: Record<string, unknown>) => this.executeMemoryExtensions(name, value),
   (name: string, value: Record<string, unknown>) => this.executeMcpAgent(name, value),
   (name: string, value: Record<string, unknown>) => this.executeNotes(name, value),
   (name: string, value: Record<string, unknown>) => this.executeWorkflows(name, value),
   (name: string, _value: Record<string, unknown>) => this.executeOverview(name)
  ]) {
   const result = await group(action, args)
   if (result.handled) return result.value
  }
  throw new Error('此研究工具尚未完成原生适配：' + action)
 }
 private async exportFile(folder: string, name: string, text: string) {
  if (!/^[a-f0-9]{32}(?:-工作流)?\.md$/.test(name) || !['研究成果'].includes(folder))
   throw new Error('研究导出名称无效')
  const directory = resolve(this.root, folder)
  await mkdir(directory, {recursive: true})
  if (
   (await lstat(directory)).isSymbolicLink() ||
   resolve(await realpath(directory)).toLowerCase() !== directory.toLowerCase()
  )
   throw new Error('研究导出目录无效')
  const path = join(directory, name)
  try {
   await writeFile(path, text, {flag: 'wx'})
  } catch (error) {
   if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
   const item = await lstat(path)
   if (!item.isFile() || item.isSymbolicLink()) throw new Error('研究导出文件无效')
  }
  const hash = (value: string) => createHash('sha256').update(value).digest('hex')
  if (hash(await readFile(path, 'utf8')) !== hash(text)) throw new Error('已有研究成果被修改，保留当前文件')
  return path
 }
 async dispose() {
  if (this.closed) return
  this.closed = true
  await this.workflows.dispose()
  await this.engine.dispose()
  await this.im?.dispose()
  this.knowledge.close()
  this.store.close()
 }
}
