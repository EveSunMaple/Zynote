import type {Highlight} from '@shared/study'
export type Rect = Highlight['rect']
export type ChapterLine = {page: number; line: number; text: string; rect: Rect; marked: boolean}
export type Fragment = {text: string; box: Rect}
export type ChapterRange = {title: string; first: number; last: number}

export const normalized = (text: string) =>
 text
  .normalize('NFKC')
  .toLowerCase()
  .replace(/[\s\p{P}]/gu, '')
const chapterToken = /^(?:第\s*[〇零一二两三四五六七八九十百千\d]+\s*章|chapter\s+(?:\d+|[ivxlcdm]+)\b)/i
export function isChapterHeading(text: string) {
 return chapterToken.test(text.normalize('NFKC').trim())
}
export function headingLine(lines: ChapterLine[], title: string) {
 const target = normalized(title)
 const number = chapterToken.exec(title.trim())?.[0]
 return (
  lines.find(row => normalized(row.text) === target) ??
  lines.find(
   row =>
    normalized(row.text).length > 2 &&
    (normalized(row.text).includes(target) || (number && normalized(row.text) === normalized(number)))
  ) ??
  lines.find(row => number && normalized(row.text).startsWith(normalized(number)) && !/(?:…|\.{3})/.test(row.text))
 )
}
const ls = (row: Fragment) => row.box[1] + row.box[3] / 2
function splitBands(sorted: Fragment[]) {
 const bands: Fragment[][] = []
 for (const part of sorted) {
  const group = bands.at(-1)
  if (group && Math.abs(ls(group[0]) - ls(part)) <= Math.max(1, Math.min(group[0].box[3], part.box[3]) * 0.44))
   group.push(part)
  else bands.push([part])
 }
 return bands
}
function mergeBand(band: Fragment[]) {
 const rows: Fragment[] = []
 let line: Fragment | undefined
 for (const part of band.toSorted((a, b) => a.box[0] - b.box[0])) {
  const [x, y, w, h] = part.box
  const gap = line ? x - line.box[0] - line.box[2] : Infinity
  if (!line || gap > Math.max(9, h * 1.5)) {
   line = {text: part.text, box: [...part.box]}
   rows.push(line)
   continue
  }
  const [a, b, c, d] = line.box
  line.text += (gap > h * 0.2 && /[a-z0-9]$/i.test(line.text) && /^[a-z0-9]/i.test(part.text) ? ' ' : '') + part.text
  line.box = [
   Math.min(a, x),
   Math.min(b, y),
   Math.max(a + c, x + w) - Math.min(a, x),
   Math.max(b + d, y + h) - Math.min(b, y)
  ]
 }
 return rows
}
export function groupFragments(fragments: Fragment[]) {
 // Visual rows are numbered top to bottom and then left to right. Wide gutters split two columns.
 const sorted = fragments
  .filter(f => f.text.trim() && f.box[2] > 0 && f.box[3] > 0)
  .sort((a, b) => ls(a) - ls(b) || a.box[0] - b.box[0])
 return splitBands(sorted).flatMap(mergeBand)
}
export function rankLine(row: ChapterLine) {
 if (row.text.trim().length < 5 || /[.…·]{2,}\s*\d+\s*$/.test(row.text) || isChapterHeading(row.text)) return 0
 if (row.marked) return 6
 if (
  /(定义|定理|性质|法则|条件|推论|例\s*[\d一二三]|解法|例题|definition|theorem|example)\b/i.test(row.text) ||
  /(定义|定理|性质|法则|条件|推论|例题|解法)/.test(row.text)
 )
  return 4
 return row.text.trim().length >= 9 ? 1 : 0
}
export function candidatesForChapter(pages: ChapterLine[][]): ChapterLine[] {
 // Keep evidence spread through the actual chapter even when page 1 has many highlights.
 const buckets = pages.map(rows =>
  rows
   .map(row => ({row, score: rankLine(row)}))
   .filter(item => item.score > 0)
   .sort((a, b) => b.score - a.score || a.row.line - b.row.line)
   .slice(0, 4)
   .map(item => item.row)
 )
 const pool: ChapterLine[] = []
 for (let rank = 0; rank < 4; rank++) {
  const round = buckets.flatMap(rows => (rows[rank] ? [rows[rank]] : []))
  const count = Math.min(96 - pool.length, round.length)
  if (count <= 0) break
  for (let i = 0; i < count; i++) pool.push(round[count === 1 ? 0 : Math.round((i * (round.length - 1)) / (count - 1))])
 }
 return pool.sort((a, b) => a.page - b.page || a.line - b.line)
}
export function isContentsPage(rows: ChapterLine[]) {
 const visible = rows.filter(row => !/^[\d\s–-]+$/.test(row.text))
 const dotEntries = visible.filter(row => /[.…·]{2,}/.test(row.text) && /\d\s*$/.test(row.text)).length
 const titled = visible.some(row => /^(目录|目次|contents|tableofcontents)$/i.test(normalized(row.text)))
 const chapters = visible.filter(row => isChapterHeading(row.text)).length
 const separateNumbers = rows.length - visible.length
 return (
  (titled && (dotEntries > 0 || chapters >= 2 || separateNumbers >= 2)) ||
  (dotEntries >= 2 && dotEntries >= visible.length * 0.45)
 )
}
