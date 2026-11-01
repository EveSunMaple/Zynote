import {useEffect, useRef, useState, type RefObject} from 'react'
import {createPortal} from 'react-dom'
import {Button} from '@heroui/react'
import type {PDFDocumentProxy, PageViewport} from 'pdfjs-dist/legacy/build/pdf.mjs'
import type {Highlight} from '@shared/study'
import type {Book} from '@renderer/features/study/types'
import {chapterRanges, pageLines, summarySpace, visualRect} from '@renderer/features/study/chapter-pages'
import {
 candidatesForChapter,
 isContentsPage,
 type ChapterRange,
 type ChapterLine,
 type Rect
} from '@renderer/features/study/chapter-lines'
import {errorMessage} from '@renderer/ui/ToastHost'
import './chapter-summary.css'

type Props = {
 document: PDFDocumentProxy | null
 book: Book
 pageNumber: number
 viewport: PageViewport | null
 annotations: readonly Highlight[]
 paper: RefObject<HTMLDivElement | null>
 onPageNumber: (page: number) => void
 onSaved: () => void
 busy?: boolean
 more?: boolean
}
type Jump = {page: number; line: number; rect: Rect}
function position(rect: Rect, v: PageViewport) {
 const [left, top, width, height] = visualRect(rect, v)
 return {left, top, width, height}
}
function validChapterRange(range: ChapterRange, pages: number) {
 return (
  Number.isInteger(range.first) &&
  Number.isInteger(range.last) &&
  range.first >= 1 &&
  range.last >= range.first &&
  range.last <= pages &&
  range.title.trim().length > 0
 )
}
export function ChapterSummary({
 document: doc,
 book,
 pageNumber,
 viewport,
 annotations,
 paper,
 onPageNumber,
 onSaved,
 busy,
 more = false
}: Readonly<Props>) {
 const [ranges, setRanges] = useState<ChapterRange[]>([])
 const [custom, setCustom] = useState<ChapterRange | null>(null)
 const [open, setOpen] = useState(false)
 const [status, setStatus] = useState(''),
  [error, setError] = useState('')
 const [draft, setDraft] = useState<Highlight | null>(null)
 const [target, setTarget] = useState<Jump | null>(null)
 const targetEl = useRef<HTMLDivElement>(null)
 const request = useRef(0)
 const inFlight = useRef(false)
 const saved = annotations.filter(row => row.chapter)
 const known = ranges.find(row => row.first <= pageNumber && pageNumber <= row.last)
 const offer = more || known?.last === pageNumber
 const existing = saved.find(row => row.page === pageNumber)
 const prior = existing?.chapter
 const range = custom ?? known ?? {title: prior?.title ?? '', first: pageNumber, last: prior?.lastPage ?? pageNumber}
 useEffect(() => {
  const token = ++request.current
  inFlight.current = false
  setRanges([])
  setCustom(null)
  setDraft(null)
  setTarget(null)
  setOpen(false)
  setError('')
  setStatus('')
  if (doc)
   void chapterRanges(doc)
    .then(rows => {
     if (token === request.current) setRanges(rows)
    })
    .catch(() => {})
  return () => {
   request.current++
  }
 }, [doc])
 useEffect(() => {
  if (target && pageNumber !== target.page) return
  if (!target || !viewport) return
  targetEl.current?.scrollIntoView({block: 'center', inline: 'nearest', behavior: 'instant'})
  const timer = window.setTimeout(() => setTarget(null), 5000)
  return () => window.clearTimeout(timer)
 }, [target, pageNumber, viewport])
 useEffect(() => {
  setOpen(false)
  setCustom(null)
  setError('')
 }, [pageNumber])
 const localDraft = draft?.page === pageNumber ? draft : null
 const display = localDraft
  ? [...saved.filter(row => row.page !== pageNumber || row.chapter?.title !== localDraft.chapter?.title), localDraft]
  : saved
 async function readChapter(source: PDFDocumentProxy, target: ChapterRange, token: number) {
  const byPage: ChapterLine[][] = []
  setStatus(`正在读取本章正文 · PDF 第 ${target.first}/${target.last} 页`)
  const firstPage = await source.getPage(target.first)
  const firstLines = await pageLines(firstPage, book.id, () => {
   if (token === request.current) setStatus(`正在识别扫描页 · PDF 第 ${target.first}/${target.last} 页`)
  })
  if (token !== request.current) return null
  if (isContentsPage(firstLines)) throw new Error('起始页是目录，请选择正文里出现章号的那一页。')
  const rect = await summarySpace(firstPage, firstLines, target.title)
  if (!isContentsPage(firstLines)) byPage.push(firstLines)
  for (let p = target.first + 1; p <= target.last; p++) {
   if (token !== request.current) return null
   setStatus(`正在读取本章正文 · PDF 第 ${p}/${target.last} 页`)
   const page = await source.getPage(p)
   const lines = await pageLines(page, book.id, () => {
    if (token === request.current) setStatus(`正在识别扫描页 · PDF 第 ${p}/${target.last} 页`)
   })
   if (token !== request.current) return null
   if (!isContentsPage(lines)) byPage.push(lines)
  }
  return {byPage, rect}
 }
 async function generate() {
  if (!doc || inFlight.current || busy) return
  const {first, last, title} = range
  if (!validChapterRange(range, book.pages)) {
   setError('请填写正文章名和这一章的实际起止页。')
   return
  }
  inFlight.current = true
  const token = request.current
  setError('')
  setDraft(null)
  try {
   const chapter = await readChapter(doc, {first, last, title}, token)
   if (!chapter) return
   const {byPage, rect} = chapter
   const candidates = candidatesForChapter(byPage)
   if (!rect || !candidates.length) throw new Error('所选章节没有找到可核对的正文行。')
   if (token !== request.current) return
   setStatus('正在整理本章重点…')
   const result = await window.assistant.invoke<{text: string; indices: number[]}>('textbook:chapter-summary', {
    id: book.id,
    first,
    last,
    candidates: candidates.map(({page, line, text, marked}) => ({page, line, text: text.slice(0, 340), marked}))
   })
   if (token !== request.current) return
   const refs = result.indices.map(i => {
    const row = candidates[i]
    if (!row) throw new Error('小结缺少可核对的正文依据。')
    return {page: row.page, line: row.line, rect: row.rect, quote: row.text.slice(0, 180)}
   })
   setDraft({
    id: crypto.randomUUID(),
    page: first,
    rect,
    source: 'ai',
    quote: title,
    evidence: result.text,
    extension: false,
    chapter: {title, text: result.text, lastPage: last, refs}
   })
   setOpen(false)
   onPageNumber(first)
  } catch (reason) {
   if (token === request.current) setError(errorMessage(reason))
  } finally {
   if (token === request.current) {
    inFlight.current = false
    setStatus('')
   }
  }
 }
 async function save() {
  if (!draft || inFlight.current || busy) return
  const token = request.current
  inFlight.current = true
  setError('')
  setStatus('正在保存章小结…')
  try {
   await window.assistant.invoke('textbook:manual', {id: book.id, highlight: draft})
   if (token !== request.current) return
   onSaved()
   setDraft(null)
  } catch (reason) {
   if (token === request.current) setError(errorMessage(reason))
  } finally {
   if (token === request.current) {
    inFlight.current = false
    setStatus('')
   }
  }
 }
 function jump(ref: Jump) {
  setTarget(ref)
  onPageNumber(ref.page)
 }
 function cancel() {
  request.current++
  inFlight.current = false
  setStatus('')
  setError('')
 }
 return (
  <div className="chapter-tools">
   {offer && !draft && !status && (
    <Button
     size="sm"
     variant={known?.last === pageNumber ? 'secondary' : 'ghost'}
     onPress={() => setOpen(v => !v)}
     aria-expanded={open}
    >
     {known?.last === pageNumber ? '读完本章 · 生成小结' : '本章小结'}
    </Button>
   )}
   {offer && open && !status && !draft && (
    <form
     className="chapter-range"
     onSubmit={event => {
      event.preventDefault()
      void generate()
     }}
    >
     <label>
      正文章名{' '}
      <input
       className="chapter-input"
       value={range.title}
       placeholder="例如 第一章 极限"
       maxLength={160}
       required
       onChange={e => setCustom({...range, title: e.target.value})}
      />
     </label>
     <label>
      首页{' '}
      <input
       className="chapter-input"
       type="number"
       min={1}
       max={book.pages}
       value={range.first}
       onChange={e => setCustom({...range, first: Number(e.target.value)})}
      />
     </label>
     <label>
      末页{' '}
      <input
       className="chapter-input"
       type="number"
       min={range.first}
       max={book.pages}
       value={range.last}
       onChange={e => setCustom({...range, last: Number(e.target.value)})}
      />
     </label>
     <Button size="sm" variant="secondary" type="submit" isDisabled={busy}>
      预览本章小结
     </Button>
     <small>按整章正文整理，扫描页会逐页识别。引用按 PDF 页内文字行从上到下、同排从左到右计数。</small>
    </form>
   )}
   {status && (
    <>
     <output>{status}</output>
     <Button variant="ghost" size="sm" onPress={cancel} isDisabled={status === '正在保存章小结…'}>
      取消整理
     </Button>
    </>
   )}
   {draft && !status && (
    <>
     <span>章小结已生成，请核对后保存。</span>
     <Button size="sm" variant="secondary" isDisabled={busy} onPress={() => void save()}>
      保存章小结到 PDF
     </Button>
     <Button size="sm" variant="ghost" onPress={() => setDraft(null)}>
      放弃
     </Button>
    </>
   )}
   {error && (
    <span role="alert" className="study-inline-error">
     {error}
    </span>
   )}
   {paper.current &&
    viewport &&
    createPortal(
     <>
      {display.map(
       row =>
        row.chapter && (
         <div
          data-pdf-interactive=""
          className="chapter-margin"
          key={row.id}
          style={{...position(row.rect, viewport), fontSize: Math.max(5, visualRect(row.rect, viewport)[2] / 19)}}
         >
          <strong>本章小结</strong>
          <span>{row.chapter.text}</span>
          <span className="chapter-links">
           {row.chapter.refs.map(ref => (
            <button
             type="button"
             className="chapter-link"
             title={ref.quote}
             key={ref.page + '/' + ref.line}
             onClick={() => jump(ref)}
            >
             P{ref.page} · 第{ref.line}行
            </button>
           ))}
          </span>
         </div>
        )
      )}
      {target?.page === pageNumber && (
       <div
        aria-label={`PDF 第${target.page}页第${target.line}行`}
        ref={targetEl}
        className="chapter-destination"
        style={position(target.rect, viewport)}
       />
      )}
     </>,
     paper.current
    )}
  </div>
 )
}
