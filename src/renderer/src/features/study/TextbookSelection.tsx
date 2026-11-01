import {useEffect, useRef, useState, type PointerEvent as ReactPointerEvent} from 'react'
import {Button} from '@heroui/react'
import {StudyNextStep} from '@renderer/features/study/StudyNextStep'
import {TextLayer, type PageViewport, type PDFPageProxy} from 'pdfjs-dist/legacy/build/pdf.mjs'
import type {Highlight} from '@shared/study'
import './textbook-selection.css'

type Point = [number, number]
export type ManualMark = {rect: Highlight['rect']; quote: string; ink?: Point[]}
export type SelectionTool = 'text' | 'box' | 'ink'
type OcrLine = {text: string; box: [number, number, number, number]}
type OcrState = {loading?: boolean; error?: string; lines?: OcrLine[]; hasNative?: boolean; showNative?: boolean}
type Drag = {pointer: number; tool: SelectionTool; points: Point[]}

function bounds(a: Point, b: Point): Highlight['rect'] {
 return [Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.abs(b[0] - a[0]), Math.abs(b[1] - a[1])]
}
function pdfPoint(x: number, y: number, area: DOMRect, view: PageViewport): Point {
 const vx = Math.max(0, Math.min(view.width, ((x - area.left) / area.width) * view.width))
 const vy = Math.max(0, Math.min(view.height, ((y - area.top) / area.height) * view.height))
 const [px, py] = view.convertToPdfPoint(vx, vy)
 return [px, py]
}
function inkBounds(points: Point[], view: PageViewport): Highlight['rect'] {
 const xs = points.map(point => point[0]),
  ys = points.map(point => point[1])
 const [x0, y0, x1, y1] = view.viewBox
 return bounds(
  [Math.max(x0, Math.min(...xs) - 1.5), Math.max(y0, Math.min(...ys) - 1.5)],
  [Math.min(x1, Math.max(...xs) + 1.5), Math.min(y1, Math.max(...ys) + 1.5)]
 )
}

function selectedText(container: HTMLElement, area: DOMRect, view: PageViewport): ManualMark[] {
 const selection = window.getSelection()
 if (!selection?.rangeCount || selection.isCollapsed || !container.contains(selection.anchorNode)) return []
 const range = selection.getRangeAt(0)
 const marks: ManualMark[] = []
 // Range.getClientRects() on a multi-line range also includes whole ancestor spans.
 // Intersect each text run separately so the first and last partial words stay exact.
 const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT)
 for (let node = walker.nextNode(); node; node = walker.nextNode()) {
  if (!range.intersectsNode(node)) continue
  const part = document.createRange()
  part.selectNodeContents(node)
  if (range.startContainer === node) part.setStart(node, range.startOffset)
  if (range.endContainer === node) part.setEnd(node, range.endOffset)
  const quote = part.toString().trim()
  if (!quote) continue
  for (const box of Array.from(part.getClientRects())) {
   if (!box.width || !box.height) continue
   const rect = bounds(pdfPoint(box.left, box.top, area, view), pdfPoint(box.right, box.bottom, area, view))
   if (rect[2] > 0 && rect[3] > 0) marks.push({rect, quote})
  }
 }
 return marks
}

function mountOcr(container: HTMLElement, lines: OcrLine[], view: PageViewport) {
 const context = document.createElement('canvas').getContext('2d')
 for (const {
  box: [left, top, right, bottom],
  text
 } of lines) {
  if (!text || right <= left || bottom <= top || !context) continue
  const span = document.createElement('span')
  span.textContent = text
  const height = (bottom - top) * view.height
  context.font = `${height}px sans-serif`
  const measured = context.measureText(text).width
  if (!measured) continue
  span.style.left = left * 100 + '%'
  span.style.top = top * 100 + '%'
  span.style.font = context.font
  span.style.lineHeight = '1'
  span.style.transform = `scaleX(${((right - left) * view.width) / measured})`
  container.append(span)
 }
}

export function useTextbookSelection(
 bookId: string | undefined,
 pageNumber: number,
 entryId: string | undefined,
 page: PDFPageProxy | null,
 view: PageViewport | null,
 enabled: boolean,
 busy: boolean,
 source: Highlight['source']
) {
 const [tool, setTool] = useState<SelectionTool>('text')
 const [marks, setMarks] = useState<ManualMark[]>([])
 const [live, setLive] = useState<Drag | null>(null)
 const [textState, setTextState] = useState<'loading' | 'ready' | 'empty' | 'failed'>('loading')
 const [ocrPages, setOcrPages] = useState<Record<string, OcrState>>({})
 const [warning, setWarning] = useState('')
 const surface = useRef<HTMLDivElement>(null),
  text = useRef<HTMLDivElement>(null)
 const drag = useRef<Drag | null>(null)
 const key = bookId + '/' + pageNumber
 const ocr = ocrPages[key]
 const displayedOcr = !ocr?.showNative && ocr?.lines?.length ? ocr.lines : undefined

 function clear() {
  drag.current = null
  setLive(null)
  setMarks([])
  setWarning('')
  const selection = window.getSelection()
  if (selection && text.current?.contains(selection.anchorNode)) selection.removeAllRanges()
 }
 useEffect(() => {
  clear()
 }, [bookId, pageNumber, entryId, tool])
 useEffect(() => {
  const container = text.current
  if (!container) return
  container.replaceChildren()
  setTextState('loading')
  if (!page || page.pageNumber !== pageNumber || !view || !enabled || tool !== 'text') return
  if (displayedOcr) {
   mountOcr(container, displayedOcr, view)
   setTextState('ready')
   return () => container.replaceChildren()
  }
  let active = true
  container.style.setProperty('--total-scale-factor', String(view.scale * view.userUnit))
  const layer = new TextLayer({container, viewport: view, textContentSource: page.streamTextContent()})
  void layer
   .render()
   .then(() => {
    if (active) setTextState(layer.textContentItemsStr.some(value => value.trim()) ? 'ready' : 'empty')
   })
   .catch(() => {
    if (active) setTextState('failed')
   })
  return () => {
   active = false
   layer.cancel()
   container.replaceChildren()
   // TextLayer sets dimensions and rotation on this container; OCR uses visual page coordinates.
   container.removeAttribute('data-main-rotation')
   container.removeAttribute('style')
  }
 }, [page, pageNumber, view, tool, enabled, displayedOcr])

 async function recognize() {
  if (!bookId || ocr?.loading || busy) return
  clear()
  if (ocr?.lines?.length) {
   setOcrPages(current => ({...current, [key]: {...current[key], showNative: false}}))
   return
  }
  const hasNative = textState === 'ready'
  setOcrPages(current => ({...current, [key]: {loading: true}}))
  try {
   const {lines} = await window.assistant.invoke<{lines: OcrLine[]}>('textbook:ocr-page', {
    id: bookId,
    page: pageNumber
   })
   setOcrPages(current => ({
    ...current,
    [key]: {lines, hasNative, error: lines.length ? undefined : '本页未识别到可选择的文字。'}
   }))
  } catch (reason) {
   setOcrPages(current => ({...current, [key]: {error: reason instanceof Error ? reason.message : String(reason)}}))
  }
 }

 function at(event: ReactPointerEvent<HTMLDivElement>): Point {
  return pdfPoint(event.clientX, event.clientY, event.currentTarget.getBoundingClientRect(), view!)
 }
 function start(event: ReactPointerEvent<HTMLDivElement>) {
  if (!enabled || busy || !page || !view || !event.isPrimary || event.button !== 0) return
  if (tool !== 'text') {
   event.preventDefault()
   event.currentTarget.setPointerCapture(event.pointerId)
   event.currentTarget.focus({preventScroll: true})
  }
  setMarks([])
  setWarning('')
  drag.current = {pointer: event.pointerId, tool, points: [at(event)]}
  setLive(drag.current)
 }
 function move(event: ReactPointerEvent<HTMLDivElement>) {
  const active = drag.current
  if (!active || active.pointer !== event.pointerId || !view) return
  if (active.tool === 'text') return
  const point = at(event),
   points = active.points,
   previous = points[points.length - 1]
  if (previous[0] === point[0] && previous[1] === point[1]) return
  const next =
   active.tool === 'box'
    ? [points[0], point]
    : [...(points.length >= 1024 ? points.filter((_, index) => index % 2 === 0) : points), point]
  drag.current = {...active, points: next}
  setLive(drag.current)
 }
 function finishText() {
  if (!view || !surface.current || !text.current) return
  const selected = selectedText(text.current, surface.current.getBoundingClientRect(), view)
  if (selected.length > 200 || selected.some(mark => [...mark.quote].length > 3000)) {
   setMarks([])
   setWarning('本次选择过长，请分段选择后保存。')
  } else setMarks(selected)
 }
 function end(event: ReactPointerEvent<HTMLDivElement>) {
  const active = drag.current
  if (!active || active.pointer !== event.pointerId || !view) return
  drag.current = null
  setLive(null)
  if (active.tool === 'text') {
   finishText()
   return
  }
  if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
  const point = at(event),
   first = active.points[0]
  if (active.tool === 'box') {
   const rect = bounds(first, point)
   if (rect[2] >= 1 && rect[3] >= 1) setMarks([{rect, quote: '手动框选区域'}])
  } else {
   const previous = active.points[active.points.length - 1]
   const points = [...active.points]
   if (previous[0] !== point[0] || previous[1] !== point[1]) {
    if (points.length === 1024) points.pop()
    points.push(point)
   }
   if (points.length > 1) setMarks([{rect: inkBounds(points, view), quote: '手动自由画线', ink: points}])
  }
 }
 function cancel() {
  drag.current = null
  setLive(null)
 }
 useEffect(() => {
  // Native text selection needs normal hit testing, so only drawing tools capture the pointer.
  const releaseOutside = (event: PointerEvent) => {
   if (drag.current?.tool !== 'text' || drag.current.pointer !== event.pointerId) return
   if (event.target instanceof Node && surface.current?.contains(event.target)) return
   cancel()
   finishText()
  }
  document.addEventListener('pointerup', releaseOutside)
  return () => document.removeEventListener('pointerup', releaseOutside)
 }, [view])

 const readingText = tool === 'text' && textState === 'loading'
 const ocrLabel = ocr?.lines?.length
  ? '显示已识别的图片文字'
  : ocr?.error
    ? '重新识别本页文字'
    : textState === 'ready'
      ? '用千问识别本页图片文字'
      : '识别本页文字'
 function nextStep() {
  if (marks.length) return '确认位置后，点「保存手动划线」。'
  if (tool === 'box') return '在教材上按住左键，拖出要标记的方框。'
  if (tool === 'ink') return '在教材上按住左键，沿需要标记的地方画线。'
  return textState === 'ready' ? '在教材上按住左键，拖选要划线的文字。' : `点「${ocrLabel}」，读取扫描页上的文字。`
 }
 const toolbar = (
  <div className="study-manual-tools">
   <div className="study-match__controls" role="group" aria-label="手动标注工具">
    {(
     [
      ['text', '选择文字'],
      ['box', '自由框选'],
      ['ink', '自由画线']
     ] as const
    ).map(([id, label]) => (
     <Button
      key={id}
      size="sm"
      variant={id === tool ? 'secondary' : 'tertiary'}
      aria-pressed={id === tool}
      onPress={() => setTool(id)}
     >
      {label}
     </Button>
    ))}
   </div>
   {(warning || (tool === 'text' && ocr?.error)) && <small role="alert">{warning || ocr?.error}</small>}
   {tool === 'text' && (ocr?.loading || readingText) ? (
    <small role="status">{ocr?.loading ? '千问正在识别本页文字…' : '正在读取本页文字层…'}</small>
   ) : (
    !busy && <StudyNextStep>{nextStep()}</StudyNextStep>
   )}
   {tool === 'text' && !displayedOcr && textState !== 'loading' && (
    <Button size="sm" variant="secondary" isDisabled={!!ocr?.loading || busy} onPress={() => void recognize()}>
     {ocrLabel}
    </Button>
   )}
   {tool === 'text' && displayedOcr && ocr?.hasNative && (
    <Button
     size="sm"
     variant="tertiary"
     onPress={() => {
      clear()
      setOcrPages(current => ({...current, [key]: {...current[key], showNative: true}}))
     }}
    >
     返回原生文字层
    </Button>
   )}
  </div>
 )
 const display = marks.map(mark =>
  mark.ink
   ? {tool: 'ink' as const, points: mark.ink}
   : {
      tool: 'box' as const,
      points: [
       [mark.rect[0], mark.rect[1]],
       [mark.rect[0] + mark.rect[2], mark.rect[1] + mark.rect[3]]
      ] as Point[]
     }
 )
 if (live) display.push({tool: live.tool === 'ink' ? 'ink' : 'box', points: live.points})
 const overlay = (
  <div
   ref={surface}
   className="study-selection-surface"
   data-study-source={source}
   data-tool={tool}
   data-enabled={enabled}
   data-text-preview={tool === 'text' && marks.length > 0 ? '' : undefined}
   role="application"
   aria-label="教材标注区域"
   tabIndex={-1}
   onPointerDown={start}
   onPointerMove={move}
   onPointerUp={end}
   onPointerCancel={cancel}
   onLostPointerCapture={cancel}
   onKeyUp={() => {
    if (tool === 'text') finishText()
   }}
  >
   <div ref={text} className={displayedOcr ? 'study-ocr-layer' : 'study-text-layer'} />
   {view && enabled && display.length > 0 && (
    <svg className="study-selection-preview" aria-hidden="true" viewBox={`0 0 ${view.width} ${view.height}`}>
     {display.map((shape, index) => {
      const points = shape.points.map(p => view.convertToViewportPoint(...p))
      const [x, y] = points[0],
       [u, v] = points[points.length - 1]
      if (points.length === 1)
       return shape.tool === 'ink' ? (
        <circle key={index} cx={x} cy={y} r={3} />
       ) : (
        <rect key={index} x={x - 2} y={y - 2} width={4} height={4} />
       )
      if (shape.tool === 'ink') return <polyline key={index} points={points.map(p => p.join(',')).join(' ')} />
      return <rect key={index} x={Math.min(x, u)} y={Math.min(y, v)} width={Math.abs(x - u)} height={Math.abs(y - v)} />
     })}
    </svg>
   )}
  </div>
 )
 return {marks, clear, toolbar, overlay, tool}
}
