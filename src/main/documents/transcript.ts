import {readFile} from 'node:fs/promises'
import {extname} from 'node:path'
import {parseDocument} from '@main/documents/documents'
import {parseSrt, milliseconds} from '@shared/srt'
import type {Segment} from '@shared/types'
export async function readTranscript(path: string) {
 if (extname(path).toLowerCase() === '.srt') {
  const text = await readFile(path, 'utf8')
  return {text, segments: parseSrt(text), estimated: false}
 }
 const parsed = await parseDocument(path)
 if (parsed.needsOcr || !parsed.text.trim()) throw new Error('原文没有可读取的文字，请导入智云导出的 Word 或文本原文')
 return splitTranscript(parsed.text)
}
function transcriptRow(block: string) {
 let value = block
 if (value.startsWith('[') || value.startsWith('【')) value = value.slice(1)
 const time = /^(\d{1,2}:\d{2}(?::\d{2})?)/.exec(value)
 if (!time) return undefined
 value = value.slice(time[0].length)
 if (value.startsWith(']') || value.startsWith('】')) value = value.slice(1)
 value = value.trimStart()
 if (value.startsWith(':') || value.startsWith('：')) value = value.slice(1).trimStart()
 return {stamp: time[1], text: value}
}
export function splitTranscript(text: string) {
 let blocks = text
  .split(/\r?\n/)
  .map(line => line.trim())
  .filter(Boolean)
 const heading = blocks.findIndex(line => /^\d+\.\s*语音识别$/.test(line))
 if (heading >= 0) blocks = blocks.slice(heading + 1)
 const rows: {text: string; stamp: number | null}[] = []
 for (const block of blocks) {
  const row = transcriptRow(block)
  let stamp: number | null = null
  if (row)
   stamp = milliseconds(
    row.stamp.split(':').length === 2 ? '00:' + row.stamp.padStart(5, '0') : row.stamp.padStart(8, '0')
   )
  const value = row ? row.text : block
  if (value) rows.push({text: value, stamp})
  else if (stamp !== null) rows.push({text: '', stamp})
 }
 const segments: Segment[] = []
 let cursor = 0,
  pending: number | null = null
 for (let i = 0; i < rows.length; i++) {
  const row = rows[i]
  if (!row.text) {
   pending = row.stamp
   continue
  }
  const start = row.stamp ?? pending ?? cursor
  pending = null
  const next = rows.slice(i + 1).find(r => r.stamp !== null)?.stamp
  const end = next !== undefined && next !== null && next > start ? next : start + Math.max(1000, row.text.length * 250)
  if (segments.length && start < segments.at(-1)!.end) segments.at(-1)!.end = start
  if (segments.length && start <= segments.at(-1)!.start) throw new Error('原文时间戳没有递增，请先检查原文')
  segments.push({start, end, text: row.text})
  cursor = end
 }
 if (!segments.length) throw new Error('语音识别原文为空')
 return {text, segments, estimated: true}
}
