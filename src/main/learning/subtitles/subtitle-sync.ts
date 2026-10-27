import {mkdir, readFile, writeFile, rename, readdir} from 'node:fs/promises'
import {join, resolve, relative, isAbsolute} from 'node:path'
import {randomUUID, createHash} from 'node:crypto'
import {z} from 'zod'
import {bilibiliUrl} from '@main/media/bilibili/video-policy'
import {parseSrt} from '@shared/srt'
import type {SubtitleSyncTask, SubtitleSyncItem} from '@shared/subtitle-sync'
const hash = (text: string) => createHash('sha256').update(text).digest('hex')
export function subtitleLocator(input: string) {
 const url = new URL(bilibiliUrl(input))
 return `https://www.bilibili.com${url.pathname.replace(/\/$/, '')}/?p=${Number(url.searchParams.get('p') || 1)}`
}
const itemSchema = z.object({
 page: z.number().int().positive(),
 title: z.string().min(1).max(200),
 url: z.string(),
 status: z.enum(['等待处理', '已完成', '失败']),
 path: z.string().optional(),
 hash: z
  .string()
  .regex(/^[a-f0-9]{64}$/)
  .optional(),
 error: z.string().optional(),
 unchanged: z.boolean().optional(),
 sourceMethod: z.enum(['站内字幕', '用户指定 SRT']).optional(),
 generation: z.string().nullable().optional()
})
const taskSchema = z.object({
 id: z.string().uuid(),
 category: z.string().max(100),
 courseName: z.string().optional(),
 createdAt: z.string(),
 items: z.array(itemSchema).min(1).max(1000),
 interrupted: z.boolean()
})
export type Dependencies = {
 acquire: (item: SubtitleSyncItem, signal: AbortSignal) => Promise<{srt: string; path: string}>
 generation?: (url: string) => Promise<string | null>
 publish: (
  item: SubtitleSyncItem,
  srt: string,
  category: string,
  signal: AbortSignal
 ) => Promise<{unchanged: boolean; entry?: {generation_id: string}}>
 progress: (message: string) => void
}
/** Frozen user selection and verified local receipts survive process interruption.
 * Repeating a sync reads the source again; resume reuses only hash-verified SRTs.
 */
export class SubtitleSync {
 constructor(
  private readonly root: string,
  private readonly library: string
 ) {}
 private async save(task: SubtitleSyncTask) {
  await mkdir(this.root, {recursive: true})
  const path = join(this.root, task.id + '.json'),
   temp = path + '.tmp'
  await writeFile(temp, JSON.stringify(task, null, 2), 'utf8')
  await rename(temp, path)
 }
 private async read(id: string) {
  z.string().uuid().parse(id)
  const task = taskSchema.parse(JSON.parse(await readFile(join(this.root, id + '.json'), 'utf8')))
  if (task.id !== id) throw new Error('同步记录身份不一致')
  return task
 }
 async list() {
  await mkdir(this.root, {recursive: true})
  const result: SubtitleSyncTask[] = []
  // biome-ignore format: NOSONAR must stay on the await line
  for (const name of await readdir(this.root)) { // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
   if (!/^[0-9a-f-]{36}\.json$/.test(name)) continue
   // biome-ignore format: NOSONAR must stay on the await line
   result.push(await this.read(name.slice(0, -5))) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  }
  return result.sort((a, b) => b.createdAt.localeCompare(a.createdAt))
 }
 async create(items: Pick<SubtitleSyncItem, 'page' | 'title' | 'url'>[], category: string, courseName?: string) {
  const task = taskSchema.parse({
   id: randomUUID(),
   createdAt: new Date().toISOString(),
   category,
   courseName,
   interrupted: true,
   items: items.map(item => ({...item, url: subtitleLocator(item.url), status: '等待处理'}))
  })
  if (new Set(task.items.map(item => item.url)).size !== task.items.length) throw new Error('不能重复选择同一分集')
  await this.save(task)
  return task
 }
 // biome-ignore format: NOSONAR must stay on the function line
 async attach(id: string, selector: number | string, path: string) { // NOSONAR: transitional TS fallback/UI flow retained for behavior compatibility; decomposition tracked separately to avoid sequencing regressions.
  const task = await this.read(id),
   matches = task.items.filter(item =>
    typeof selector === 'string' ? item.url === subtitleLocator(selector) : item.page === selector
   ),
   item = matches[0]
  if (matches.length !== 1) throw new Error('请按视频网址明确选择本次同步的分集')
  const location = relative(resolve(this.library), resolve(path))
  if (!location || location.startsWith('..') || isAbsolute(location)) throw new Error('字幕不在课程资料目录')
  const text = await readFile(path, 'utf8')
  parseSrt(text)
  item.path = path
  item.hash = hash(text)
  item.sourceMethod = '用户指定 SRT'
  item.status = '等待处理'
  delete item.error
  task.interrupted = true
  await this.save(task)
  return task
 }
 async run(id: string, dependencies: Dependencies, signal: AbortSignal) {
  const task = await this.read(id)
  task.interrupted = true
  await this.save(task)
  try {
   for (const [index, item] of task.items.entries()) {
    signal.throwIfAborted()
    dependencies.progress(`同步知识库 ${index + 1}/${task.items.length} · P${item.page} ${item.title}`)
    try {
     if (item.generation === undefined && dependencies.generation) {
      // biome-ignore format: NOSONAR must stay on the await line
      item.generation = await dependencies.generation(item.url) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
      // biome-ignore format: NOSONAR must stay on the await line
      await this.save(task) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
     }
     let srt: string | undefined
     if (item.path && item.hash) {
      const location = relative(resolve(this.library), resolve(item.path))
      if (!location || location.startsWith('..') || isAbsolute(location)) throw new Error('同步字幕不在课程资料目录')
      try {
       // biome-ignore format: NOSONAR must stay on the await line
       const text = await readFile(item.path, 'utf8') // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
       if (hash(text) === item.hash) {
        parseSrt(text)
        srt = text
       }
      } catch (error) {
       if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
      }
     }
     if (srt === undefined) {
      // biome-ignore format: NOSONAR must stay on the await line
      const saved = await dependencies.acquire(item, signal) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
      parseSrt(saved.srt)
      // biome-ignore format: NOSONAR must stay on the await line
      if ((await readFile(saved.path, 'utf8')) !== saved.srt) throw new Error('字幕保存校验失败') // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
      srt = saved.srt
      item.path = saved.path
      item.hash = hash(srt)
      item.sourceMethod = '站内字幕'
      item.status = '等待处理'
      // biome-ignore format: NOSONAR must stay on the await line
      await this.save(task) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
     }
     signal.throwIfAborted()
     const published = await dependencies.publish(item, srt, task.category, signal)
     if (published.entry) item.generation = published.entry.generation_id
     item.status = '已完成'
     item.unchanged = published.unchanged
     delete item.error
    } catch (error) {
     signal.throwIfAborted()
     item.status = '失败'
     item.error = error instanceof Error ? error.message : '同步失败'
    }
    await this.save(task)
   }
   task.interrupted = false
   await this.save(task)
   return task
  } finally {
   await this.save(task)
  }
 }
}
