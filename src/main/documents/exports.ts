import {BrowserWindow, app} from 'electron'
import {writeFile, rm} from 'node:fs/promises'
import {Document, Packer, Paragraph, TextRun} from 'docx'
import type {LectureDetail, Course} from '@shared/types'
import {buildCourseEvidenceBundle, courseEvidenceMarkdown} from '@shared/course-evidence'
import {time} from '@shared/srt'
import {reserveFile} from '@main/library/library'
export const exportFormats = {
 subtitles: ['srt', 'txt'],
 notes: ['md', 'docx'],
 practice: ['md', 'docx', 'pdf'],
 'evidence-bundle': ['json', 'md']
} as const
export type ExportKind = keyof typeof exportFormats
export function validateExportFormat(kind: ExportKind, format: string) {
 if (!(exportFormats[kind] as readonly string[]).includes(format)) throw new Error('不支持的导出格式')
}
function notesMarkdown(detail: LectureDetail): string[] {
 if (!detail.notes) throw new Error('还没有听课笔记')
 const out: string[] = []
 const groups = [
  ['教师明确重点', detail.notes.keyPoints],
  ['明确不考', detail.notes.notTested],
  ['表态含糊', detail.notes.uncertain || []]
 ] as const
 for (const [title, points] of groups) {
  out.push(`## ${title}`, '')
  for (const p of points) {
   const levelPrefix = 'level' in p ? `[${p.level}] ` : ''
   out.push(
    `### ${levelPrefix}${p.topic}`,
    `${p.timestamp} · ${p.verified ? '原话已核验' : '⚠ 疑似编造或时间不符'}`,
    '',
    `> ${p.quote}`,
    '',
    'detail' in p ? p.detail : '',
    'pptRef' in p && p.pptRef ? `课件：${p.pptRef}` : '',
    ''
   )
  }
 }
 return out
}
function practiceAttemptText(q: NonNullable<LectureDetail['practice']>['past'][number]) {
 if (!q.attempt) return ''
 let verdict = '待自查'
 if (q.attempt.verdict === 'correct') verdict = '正确'
 else if (q.attempt.verdict === 'incorrect') verdict = '需要再练习'
 return `我的回答：${q.attempt.response}\n判定：${verdict}\n${q.attempt.feedback}`
}
function practiceQuestionMarkdown(q: NonNullable<LectureDetail['practice']>['past'][number], index: number) {
 const answerLabel = q.aiAnswer ? 'AI 生成，仅供参考' : '参考答案'
 const answerText = q.answer ? `${answerLabel}：\n${q.answer}` : '暂无答案'
 return [
  `### ${index + 1}. ${q.topic} · ${q.type}`,
  '',
  q.stem,
  '',
  `来源：${q.sourceUrl || q.sourceFile || ''}${q.year ? ' · ' + q.year : ''}`,
  `关联：${q.reason}`,
  '',
  answerText,
  q.answerSourceUrl ? `答案来源：${q.answerSourceUrl}` : '',
  q.explanation ? `详解（AI 辅助整理）：\n${q.explanation}` : '',
  practiceAttemptText(q),
  ''
 ]
}
function practiceMarkdown(detail: LectureDetail): string[] {
 if (!detail.practice) throw new Error('还没有练习题')
 const out: string[] = []
 for (const [title, items] of [
  ['历年真题', detail.practice.past],
  ['有来源的拓展练习', detail.practice.extended],
  ['AI 自测题（非历年真题）', detail.practice.generated || []]
 ] as const) {
  out.push(`## ${title}（${items.length} 道）`, '')
  items.forEach((q, i) => out.push(...practiceQuestionMarkdown(q, i)))
 }
 if (detail.practice.shortage) out.push(`拓展题不足 ${detail.practice.shortage} 道，未用 AI 编题冒充搜索结果。`)
 return out
}
export function markdown(detail: LectureDetail, kind: 'notes' | 'practice'): string {
 const out = [`# ${detail.lecture.title} · ${kind === 'notes' ? '听课笔记' : '课后练习'}`, '']
 out.push(...(kind === 'notes' ? notesMarkdown(detail) : practiceMarkdown(detail)))
 return out.join('\n')
}
function exportText(detail: LectureDetail, kind: ExportKind, format: string, course?: Course) {
 if (kind === 'evidence-bundle') {
  const bundle = buildCourseEvidenceBundle(course!, detail, new Date().toISOString(), app.getVersion())
  return format === 'md' ? courseEvidenceMarkdown(bundle) : JSON.stringify(bundle, null, 2)
 }
 return kind === 'subtitles' ? '' : markdown(detail, kind)
}
async function writeDocx(path: string, text: string) {
 const doc = new Document({
  sections: [
   {
    properties: {},
    children: text.split('\n').map(
     line =>
      new Paragraph({
       children: [
        new TextRun({
         text: line.replace(/^#{1,3} /, ''),
         bold: line.startsWith('#'),
         size: line.startsWith('# ') ? 32 : 22
        })
       ],
       spacing: {after: 120}
      })
    )
   }
  ]
 })
 await writeFile(path, await Packer.toBuffer(doc))
}
async function writePdf(path: string, text: string) {
 const escape = (s: string) => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
 const print = new BrowserWindow({
  show: false,
  webPreferences: {nodeIntegration: false, contextIsolation: true, sandbox: true}
 })
 try {
  await print.loadURL(
   'data:text/html;charset=utf-8,' +
    encodeURIComponent(
     `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><style>body{font:12pt 'Microsoft YaHei',sans-serif;line-height:1.6;padding:24px}pre{white-space:pre-wrap;word-break:break-word;font:inherit}</style><pre>${escape(text)}</pre>`
    )
  )
  await writeFile(path, await print.webContents.printToPDF({printBackground: true, pageSize: 'A4'}))
 } finally {
  print.destroy()
 }
}
export async function exportFile(
 detail: LectureDetail,
 kind: ExportKind,
 format: string,
 directory: string,
 course?: Course
) {
 validateExportFormat(kind, format)
 const filename = detail.lecture.title.replace(/[<>:"/\\|?*]/g, '_')
 if (kind === 'subtitles' && !detail.segments.length) throw new Error('没有字幕可导出')
 if (kind === 'evidence-bundle' && !course) throw new Error('缺少课程元信息，无法导出课程学习证据包')
 const text = exportText(detail, kind, format, course)
 const path = await reserveFile(directory, `${filename}-${kind}.${format}`)
 try {
  if (kind === 'subtitles') {
   await writeFile(
    path,
    format === 'srt' ? detail.srt : detail.segments.map(s => `[${time(s.start, false)}] ${s.text}`).join('\n'),
    'utf8'
   )
   return path
  }
  if (format === 'md' || format === 'json') await writeFile(path, text, 'utf8')
  else if (format === 'docx') await writeDocx(path, text)
  else await writePdf(path, text)
  return path
 } catch (error) {
  await rm(path, {force: true})
  throw error
 }
}
