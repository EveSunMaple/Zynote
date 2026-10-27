import {readFile, copyFile, mkdir} from 'node:fs/promises'
import {join, extname, basename, posix, resolve} from 'node:path'
import {randomUUID, createHash} from 'node:crypto'
import JSZip from 'jszip'
import mammoth from 'mammoth'
import jschardet from 'jschardet'
import iconv from 'iconv-lite'
import type {CourseFile} from '@shared/types'

export interface CoursewareSourceIdentity {
 path: string
 sha256: string
 size: number
}
export type CoursewareProvenance = 'local' | 'ocr' | 'manual'
export interface CoursewareCacheMetadata {
 version: 1
 sourcePath: string
 sourceSha256: string
 sourceSize: number
 textSha256: string
 provenance: CoursewareProvenance
 needsOcr: boolean
 omissions: string[]
 capturedAt: string
}
export interface ParsedCoursewareCache {
 text: string
 state: 'verified' | 'unknown' | 'stale'
 provenance: CoursewareProvenance | 'unknown'
 metadata?: CoursewareCacheMetadata
 needsOcr: boolean
 omissions: string[]
}
const coursewareCacheMarker = 'bilisum-courseware-cache:v1:'
const sha256 = (value: Buffer | string) => createHash('sha256').update(value).digest('hex')
export function coursewareTextHash(text: string) {
 return sha256(text)
}
export async function sourceIdentity(path: string): Promise<CoursewareSourceIdentity> {
 const bytes = await readFile(path)
 return {path: resolve(path), sha256: sha256(bytes), size: bytes.length}
}
export const coursewareSourceIdentity = sourceIdentity
export async function serializeCoursewareSourceCache(
 path: string,
 text: string,
 provenance: CoursewareProvenance,
 needsOcr: boolean,
 omissions: string[] = []
): Promise<string> {
 return serializeCoursewareCache(
  text,
  createCoursewareMetadata(await coursewareSourceIdentity(path), text, provenance, needsOcr, omissions)
 )
}
export function createCoursewareMetadata(
 source: CoursewareSourceIdentity,
 text: string,
 provenance: CoursewareProvenance,
 needsOcr: boolean,
 omissions: string[] = []
): CoursewareCacheMetadata {
 return {
  version: 1,
  sourcePath: source.path,
  sourceSha256: source.sha256,
  sourceSize: source.size,
  textSha256: coursewareTextHash(text),
  provenance,
  needsOcr,
  omissions: [...new Set(omissions)],
  capturedAt: new Date().toISOString()
 }
}
export function serializeCoursewareCache(text: string, metadata: CoursewareCacheMetadata): string {
 const saved = {...metadata, textSha256: coursewareTextHash(text)}
 return `${text}\n<!-- ${coursewareCacheMarker}${Buffer.from(JSON.stringify(saved), 'utf8').toString('base64')} -->`
}
function validCoursewareMetadata(value: unknown): value is CoursewareCacheMetadata {
 if (!value || typeof value !== 'object') return false
 const item = value as Partial<CoursewareCacheMetadata>
 return (
  item.version === 1 &&
  typeof item.sourcePath === 'string' &&
  item.sourcePath.length > 0 &&
  typeof item.sourceSha256 === 'string' &&
  /^[a-f0-9]{64}$/.test(item.sourceSha256) &&
  typeof item.sourceSize === 'number' &&
  Number.isSafeInteger(item.sourceSize) &&
  item.sourceSize >= 0 &&
  typeof item.textSha256 === 'string' &&
  /^[a-f0-9]{64}$/.test(item.textSha256) &&
  (item.provenance === 'local' || item.provenance === 'ocr' || item.provenance === 'manual') &&
  typeof item.needsOcr === 'boolean' &&
  Array.isArray(item.omissions) &&
  item.omissions.every(v => typeof v === 'string') &&
  typeof item.capturedAt === 'string'
 )
}
export function parseCoursewareCache(
 value: string,
 currentSource?: CoursewareSourceIdentity | null
): ParsedCoursewareCache {
 const match = /\n?<!-- bilisum-courseware-cache:v1:([A-Za-z0-9+/=]+) -->\s*$/.exec(value)
 if (!match) return {text: value, state: 'unknown', provenance: 'unknown', needsOcr: false, omissions: []}
 let metadata: unknown
 try {
  metadata = JSON.parse(Buffer.from(match[1], 'base64').toString('utf8'))
 } catch {
  return {text: value, state: 'unknown', provenance: 'unknown', needsOcr: false, omissions: []}
 }
 if (!validCoursewareMetadata(metadata))
  return {text: value, state: 'unknown', provenance: 'unknown', needsOcr: false, omissions: []}
 const text = value.slice(0, match.index)
 const textChanged = coursewareTextHash(text) !== metadata.textSha256
 const sourceChanged =
  currentSource === null ||
  (currentSource !== undefined &&
   (resolve(currentSource.path) !== resolve(metadata.sourcePath) ||
    currentSource.sha256 !== metadata.sourceSha256 ||
    currentSource.size !== metadata.sourceSize))
 let state: 'stale' | 'verified' | 'unknown' = 'unknown'
 if (textChanged || sourceChanged) state = 'stale'
 else if (currentSource) state = 'verified'
 return {
  text,
  state,
  provenance: metadata.provenance,
  metadata,
  needsOcr: metadata.needsOcr,
  omissions: [...metadata.omissions]
 }
}
export function coursewareCacheLimitations(cache: ParsedCoursewareCache): string[] {
 const result = [...cache.omissions]
 if (cache.state === 'stale')
  result.push('已保存的课件文本或来源文件与提取记录不匹配；保留现有文本供参考，来源需要核对。')
 else if (cache.state === 'unknown' && cache.text.trim())
  result.push('课件文本没有可验证的来源记录；已保留原文本，来源链未核实。')
 if (cache.needsOcr) result.push('课件来源需要 OCR；当前文字可能不完整。')
 if (cache.provenance === 'ocr') result.push('课件文字由 OCR 识别，未经人工逐页核对。')
 return [...new Set(result)]
}
export interface CoursewareParseResult {
 text: string
 needsOcr: boolean
 images: {label: string; url: string}[]
 omissions: string[]
 metadata: CoursewareCacheMetadata
}
export function serializeCourseware(parsed: Pick<CoursewareParseResult, 'text' | 'metadata'>): string {
 return serializeCoursewareCache(parsed.text, parsed.metadata)
}
async function withCoursewareMetadata(
 path: string,
 result: {text: string; needsOcr: boolean; images: {label: string; url: string}[]; omissions: string[]}
): Promise<CoursewareParseResult> {
 const source = await coursewareSourceIdentity(path)
 return {...result, metadata: createCoursewareMetadata(source, result.text, 'local', result.needsOcr, result.omissions)}
}
export function chunks(text: string, size = 14000): string[] {
 const paragraphs = text.split(/\n(?=#{1,6}\s|第[一二三四五六七八九十\d]+[章节])|\n\s*\n/)
 const result: string[] = []
 let current = ''
 const flush = () => {
  if (current) {
   result.push(current)
   current = ''
  }
 }
 for (const para of paragraphs) {
  if (current.length + para.length > size && current) flush()
  if (para.length > size) {
   flush()
   for (let i = 0; i < para.length; i += size) result.push(para.slice(i, i + size))
  } else current += (current ? '\n\n' : '') + para
 }
 flush()
 return result
}
export async function parseDocument(path: string): Promise<{text: string; needsOcr: boolean}> {
 const ext = extname(path).toLowerCase(),
  data = await readFile(path)
 if (ext === '.pdf') {
  const pdfjs = await import('pdfjs-dist/legacy/build/pdf.mjs')
  const task = pdfjs.getDocument({data: new Uint8Array(data), useSystemFonts: true})
  const pdf = await task.promise
  let text = ''
  try {
   for (let i = 1; i <= pdf.numPages; i++) {
    // biome-ignore format: NOSONAR must stay on the await line
    const page = await pdf.getPage(i), // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
     // biome-ignore format: NOSONAR must stay on the await line
     content = await page.getTextContent() // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
    const textItem = (item: unknown) => {
     if (typeof item !== 'object' || item === null) return ''
     const value = item as {str?: unknown; hasEOL?: boolean}
     return typeof value.str === 'string' ? value.str + (value.hasEOL ? '\n' : ' ') : '' // NOSONAR: conditional structure is intentionally compact and behavior-bearing.
    }
    text += `\n[第 ${i} 页]\n` + content.items.map(textItem).join('')
   }
  } finally {
   await task.destroy()
  }
  const real = text.replace(/\[第 \d+ 页\]/g, '').replace(/\s/g, '')
  return {text, needsOcr: real.length < Math.max(20, pdf.numPages * 10)}
 }
 if (ext === '.docx') {
  try {
   return {text: (await mammoth.extractRawText({buffer: data})).value, needsOcr: false}
  } catch {
   const zip = await JSZip.loadAsync(data),
    document = zip.file('word/document.xml')
   if (!document) throw new Error('Word 原文文件损坏')
   const xml = await document.async('string'),
    text = xml
     .replace(/<w:br[^>]*>/g, '\n')
     .replaceAll('</w:p>', '\n\n')
     .replace(/<[^<>]+>/g, '')
     .replace(/&#x([\da-f]+);/gi, (_m, n) => String.fromCodePoint(Number.parseInt(n, 16)))
     .replace(/&#(\d+);/g, (_m, n) => String.fromCodePoint(Number(n)))
     .replaceAll('&lt;', '<')
     .replaceAll('&gt;', '>')
     .replaceAll('&quot;', '"')
     .replaceAll('&apos;', "'")
     .replaceAll('&amp;', '&')
   return {text, needsOcr: false}
  }
 }
 if (!['.txt', '.md'].includes(ext)) throw new Error('资料只支持 PDF、DOCX、TXT 和 Markdown；PPT 请先另存为 PDF')
 let encoding = jschardet.detect(data).encoding || 'UTF-8'
 if (
  /gb|big5|windows-1252|iso-8859/i.test(encoding) &&
  !new TextDecoder('utf-8', {fatal: false}).decode(data).includes('�')
 )
  encoding = 'UTF-8'
 else if (/windows-1252|iso-8859/i.test(encoding)) encoding = 'GB18030'
 return {text: iconv.decode(data, iconv.encodingExists(encoding) ? encoding : 'GB18030'), needsOcr: false}
}
export async function copyInto(
 root: string,
 courseId: string,
 source: string,
 subdir = 'materials',
 directory?: string
): Promise<string> {
 const dir = directory || join(root, 'courses', courseId, subdir)
 await mkdir(dir, {recursive: true})
 const ext = extname(source),
  name = basename(source, ext)
 const dest = join(dir, (directory ? name + '-' : '') + randomUUID() + ext.toLowerCase())
 await copyFile(source, dest)
 return dest
}
export async function importDocument(
 root: string,
 courseId: string,
 kind: CourseFile['kind'],
 source: string,
 directory?: string
): Promise<CourseFile> {
 const parsed = await parseDocument(source),
  path = await copyInto(root, courseId, source, 'materials', directory)
 return {
  id: randomUUID(),
  course_id: courseId,
  kind,
  filename: basename(source),
  path,
  text: parsed.text,
  chunks: chunks(parsed.text),
  needs_ocr: parsed.needsOcr ? 1 : 0
 }
}

function decodeCoursewareXml(value: string) {
 return value
  .replace(/&#x([\da-f]+);/gi, (_m, n) => String.fromCodePoint(Number.parseInt(n, 16)))
  .replace(/&#(\d+);/g, (_m, n) => String.fromCodePoint(Number(n)))
  .replaceAll('&lt;', '<')
  .replaceAll('&gt;', '>')
  .replaceAll('&quot;', '"')
  .replaceAll('&apos;', "'")
  .replaceAll('&amp;', '&')
}
function coursewareAttr(xml: string, name: string) {
 return new RegExp(String.raw`\b${name}=["']([^"']+)["']`).exec(xml)?.[1]
}
function coursewareRelationships(xml: string) {
 return Array.from(xml.matchAll(/<Relationship\b[^>]*>/g), m => m[0])
}
function coursewarePartTarget(parent: string, value: string) {
 const target = decodeCoursewareXml(value)
 return posix.normalize(target.startsWith('/') ? target.slice(1) : posix.join(parent, target))
}
function coursewareDeclaredSize(entry: JSZip.JSZipObject) {
 const size = (entry as unknown as {_data?: {uncompressedSize?: unknown}})._data?.uncompressedSize
 return typeof size === 'number' && Number.isSafeInteger(size) && size >= 0 ? size : undefined
}
async function readCoursewareEntry(entry: JSZip.JSZipObject, limit: number, label: string) {
 const declared = coursewareDeclaredSize(entry)
 if (declared === undefined || declared > limit) throw new Error(`${label}超过 ${limit} 字节本地解析上限或大小无法验证`)
 type Worker = {error(error: Error): boolean}
 type Stream = {
  on(event: 'data' | 'error' | 'end', listener: (value?: unknown) => void): Stream
  resume(): void
  pause(): void
  _worker: Worker
 }
 // JSZip's Node stream adapter does not cancel its decompressor on destroy; stop the worker chain on overflow.
 const stream = (entry as unknown as {internalStream(type: string): Stream}).internalStream('nodebuffer')
 const maxBytes = Math.min(limit, declared)
 return await new Promise<Buffer>((resolve, reject) => {
  const parts: Buffer[] = []
  let total = 0,
   settled = false
  const fail = (error: Error) => {
   if (settled) return
   settled = true
   stream.pause()
   queueMicrotask(() => {
    try {
     stream._worker.error(error)
    } catch {}
    reject(error)
   })
  }
  stream
   .on('data', value => {
    if (settled) return
    const bytes = Buffer.isBuffer(value) ? value : Buffer.from(value as Uint8Array)
    total += bytes.length
    if (total > maxBytes) {
     fail(new Error(`${label}实际展开超过声明或本地上限 ${maxBytes} 字节`))
     return
    }
    parts.push(bytes)
   })
   .on('error', value => {
    if (!settled) {
     settled = true
     reject(value instanceof Error ? value : new Error(`${label}展开失败`))
    }
   })
   .on('end', () => {
    if (settled) return
    settled = true
    if (total !== declared) reject(new Error(`${label}实际展开大小与压缩目录不一致`))
    else resolve(Buffer.concat(parts, total))
   })
  stream.resume()
 })
}
function coursewareSlideNames(presentation: string, presentationRels: string) {
 const slideLinks = coursewareRelationships(presentationRels)
 return Array.from(presentation.matchAll(/<p:sldId\b[^>]*>/g), m => coursewareAttr(m[0], 'r:id'))
  .filter((id): id is string => !!id)
  .map(id => {
   const rel = slideLinks.find(row => coursewareAttr(row, 'Id') === id)
   if (!rel || coursewareAttr(rel, 'TargetMode') === 'External') throw new Error('PPTX 页面引用无效')
   const name = coursewarePartTarget('ppt', coursewareAttr(rel, 'Target') || '')
   if (!name.startsWith('ppt/slides/') || name.includes('../')) throw new Error('PPTX 页面路径无效')
   return name
  })
}
function coursewareMime(extension: string) {
 return {
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.gif': 'image/gif'
 }[extension]
}
// biome-ignore format: NOSONAR must stay on the function line
async function appendCoursewareSlideImages( // NOSONAR: cohesive flow; further extraction would risk behavior changes.
 zip: JSZip,
 xml: string,
 slideRel: string,
 name: string,
 index: number,
 rawImageBytes: number,
 images: {label: string; url: string}[],
 omissions: string[]
) {
 const relationRows = coursewareRelationships(slideRel)
 const seen = new Set<string>()
 const pageLabel = `课件第 ${index + 1} 页`
 for (const id of new Set(Array.from(xml.matchAll(/<a:blip\b[^>]*\br:embed=["']([^"']+)["']/g), m => m[1]))) {
  const relation = relationRows.find(row => coursewareAttr(row, 'Id') === id)
  if (!relation || coursewareAttr(relation, 'TargetMode') === 'External') continue
  const mediaName = coursewarePartTarget(posix.dirname(name), coursewareAttr(relation, 'Target') || '')
  const mime = coursewareMime(extname(mediaName).toLowerCase())
  if (!mediaName.startsWith('ppt/media/') || mediaName.includes('../') || !mime) {
   omissions.push(`第 ${index + 1} 页有未支持的嵌入图片格式`)
   continue
  }
  if (seen.has(mediaName)) continue
  seen.add(mediaName)
  const entry = zip.file(mediaName)
  if (!entry) {
   omissions.push(`第 ${index + 1} 页嵌入图片缺失`)
   continue
  }
  const remainingBytes = 8 * 1024 * 1024 - rawImageBytes
  const entrySize = coursewareDeclaredSize(entry)
  if (images.length >= 40) {
   omissions.push(`第 ${index + 1} 页嵌入图片因 40 张数量上限未发送`)
   continue
  }
  if (remainingBytes <= 0) {
   omissions.push(`第 ${index + 1} 页嵌入图片因 8 MB 图片总量上限未读取或发送`)
   continue
  }
  if (entrySize === undefined) {
   omissions.push(`第 ${index + 1} 页嵌入图片展开大小无法验证，未读取或发送`)
   continue
  }
  if (entrySize > 4 * 1024 * 1024) {
   omissions.push(`第 ${index + 1} 页嵌入图片超过 4 MB 单张上限，未读取或发送`)
   continue
  }
  if (entrySize > remainingBytes) {
   omissions.push(`第 ${index + 1} 页嵌入图片超过剩余图片预算 ${remainingBytes} 字节，未读取或发送`)
   continue
  }
  // biome-ignore format: NOSONAR must stay on the await line
  const data = await readCoursewareEntry(entry, Math.min(4 * 1024 * 1024, remainingBytes), `PPTX 第 ${index + 1} 页嵌入图片`) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  rawImageBytes += data.length
  images.push({
   label: `${pageLabel}嵌入图片 ${images.filter(image => image.label.startsWith(pageLabel)).length + 1}`,
   url: `data:${mime};base64,${data.toString('base64')}`
  })
 }
 return rawImageBytes
}
async function readCoursewareSlide(
 zip: JSZip,
 name: string,
 index: number,
 editableChars: number,
 rawImageBytes: number,
 images: {label: string; url: string}[],
 omissions: string[]
) {
 const entry = zip.file(name)
 if (!entry) throw new Error(`PPTX 第 ${index + 1} 页内容缺失`)
 // biome-ignore format: NOSONAR must stay on the await line
 const xml = (await readCoursewareEntry(entry, 5 * 1024 * 1024, `PPTX 第 ${index + 1} 页内容`)).toString('utf8') // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
 const extracted = Array.from(xml.matchAll(/<a:t(?:\s[^>]*)?>([\s\S]*?)<\/a:t>/g), m => decodeCoursewareXml(m[1]))
  .join('\n')
  .trim()
 const remaining = Math.max(0, 200000 - editableChars)
 const text = extracted.slice(0, remaining)
 if (text.length < extracted.length) omissions.push('课件可编辑文本超过 200,000 字，本地解析仅保留前段内容')
 const page = `[第 ${index + 1} 页]\n${text || '（未提取到可编辑文字）'}`
 const relName = posix.join(posix.dirname(name), '_rels', posix.basename(name) + '.rels')
 const slideRelEntry = zip.file(relName)
 const slideRel = slideRelEntry
  ? // biome-ignore format: NOSONAR must stay on the await line
    (await readCoursewareEntry(slideRelEntry, 5 * 1024 * 1024, `PPTX 第 ${index + 1} 页关系`)).toString('utf8') // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  : undefined
 if (!slideRel) return {page, used: text.length, rawImageBytes}
 rawImageBytes = await appendCoursewareSlideImages(zip, xml, slideRel, name, index, rawImageBytes, images, omissions)
 if (/<p:graphicFrame\b|<p:oleObj\b|<p:contentPart\b/.test(xml))
  omissions.push(`第 ${index + 1} 页含图表、SmartArt 或嵌入对象；仅提取其中的可编辑文本，未进行完整页面渲染`)
 if (!text && !images.some(image => image.label.startsWith(`课件第 ${index + 1} 页`)))
  omissions.push(`第 ${index + 1} 页没有提取到文字或可发送的图片`)
 return {page, used: text.length, rawImageBytes}
}
export async function parseCourseware(path: string): Promise<CoursewareParseResult> {
 const ext = extname(path).toLowerCase()
 if (ext === '.ppt') throw new Error('旧版 PPT 暂不支持直接解析，请在 PowerPoint 中另存为 PPTX')
 if (ext !== '.pptx')
  return withCoursewareMetadata(path, {
   ...(await parseDocument(path)),
   images: [] as {label: string; url: string}[],
   omissions: [] as string[]
  })
 const archive = await readFile(path)
 if (archive.length > 200 * 1024 * 1024) throw new Error('PPTX 文件超过 200 MB 本地读取上限')
 const zip = await JSZip.loadAsync(archive)
 const presentationEntry = zip.file('ppt/presentation.xml'),
  presentationRelsEntry = zip.file('ppt/_rels/presentation.xml.rels')
 if (!presentationEntry || !presentationRelsEntry) throw new Error('PPTX 缺少有效的页面目录')
 const presentation = (await readCoursewareEntry(presentationEntry, 5 * 1024 * 1024, 'PPTX 页面目录')).toString('utf8')
 const presentationRels = (await readCoursewareEntry(presentationRelsEntry, 5 * 1024 * 1024, 'PPTX 页面关系')).toString(
  'utf8'
 )
 const slideNames = coursewareSlideNames(presentation, presentationRels)
 if (!slideNames.length || slideNames.length > 500) throw new Error('PPTX 页面数须在 1–500 页之间')
 const pages: string[] = [],
  images: {label: string; url: string}[] = [],
  omissions: string[] = []
 let rawImageBytes = 0,
  editableChars = 0
 for (const [index, name] of slideNames.entries()) {
  const slide = await readCoursewareSlide(zip, name, index, editableChars, rawImageBytes, images, omissions) // NOSONAR: await inside loop is required for bounded ordered I/O.
  editableChars += slide.used
  rawImageBytes = slide.rawImageBytes
  pages.push(slide.page)
 }
 if (images.length) omissions.push('PPTX 嵌入图片以原图附加；没有还原幻灯片版式、裁切、遮罩、动画或位置关系')
 return withCoursewareMetadata(path, {
  text: pages.join('\n\n'),
  needsOcr: false,
  images,
  omissions: [...new Set(omissions)]
 })
}
