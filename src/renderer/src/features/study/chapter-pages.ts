import {OPS, type PDFPageProxy, type PageViewport, type PDFDocumentProxy} from 'pdfjs-dist/legacy/build/pdf.mjs'
import {bookmarkPage, textbookBookmarks} from '@renderer/features/study/textbook-bookmarks'
import {
 groupFragments,
 headingLine,
 isChapterHeading,
 type ChapterLine,
 type ChapterRange,
 type Fragment,
 type Rect
} from '@renderer/features/study/chapter-lines'

function bounds(points: number[][]): Rect {
 const x = points.map(p => p[0]),
  y = points.map(p => p[1])
 return [Math.min(...x), Math.min(...y), Math.max(...x) - Math.min(...x), Math.max(...y) - Math.min(...y)]
}
export function visualRect(rect: Rect, view: PageViewport): Rect {
 const [x, y, w, h] = rect
 return bounds([view.convertToViewportPoint(x, y), view.convertToViewportPoint(x + w, y + h)])
}
function pdfRect(rect: Rect, view: PageViewport): Rect {
 const [x, y, w, h] = rect
 return bounds([view.convertToPdfPoint(x, y), view.convertToPdfPoint(x + w, y + h)])
}
export async function chapterRanges(doc: PDFDocumentProxy): Promise<ChapterRange[]> {
 const flat = textbookBookmarks(await doc.getOutline())
 const headings = flat.filter(row => isChapterHeading(row.label))
 const targets = await Promise.all(
  headings.map(async row => ({title: row.label, first: await bookmarkPage(doc, row.dest).catch(() => null)}))
 )
 const starts = targets
  .filter((row): row is {title: string; first: number} => !!row.first)
  .sort((a, b) => a.first - b.first)
 // End at any following peer boundary (also appendix/index) so the last chapter doesn't consume it.
 const peers = flat.filter(row => headings.some(h => h.parent === row.parent))
 const ends = (await Promise.all(peers.map(row => bookmarkPage(doc, row.dest).catch(() => null))))
  .filter((p): p is number => p !== null)
  .sort((a, b) => a - b)
 return starts.map(row => ({...row, last: (ends.find(p => p > row.first) ?? doc.numPages + 1) - 1}))
}
export async function pageLines(page: PDFPageProxy, bookId: string, onOcr: () => void): Promise<ChapterLine[]> {
 const view = page.getViewport({scale: 1})
 const data = await page.getTextContent()
 let parts: Fragment[] = []
 for (const item of data.items) {
  if (!('str' in item) || !item.str.trim()) continue
  const [a, b, c, d, x, y] = item.transform
  const norm = Math.hypot(a, b) || 1
  const [dx, dy] = [(a * item.width) / norm, (b * item.width) / norm]
  const {ascent, descent} = data.styles[item.fontName] ?? {}
  const top = ascent ?? 0.85,
   bottom = descent ?? -0.15
  const points = [
   view.convertToViewportPoint(x + c * top, y + d * top),
   view.convertToViewportPoint(x + c * bottom, y + d * bottom),
   view.convertToViewportPoint(x + dx + c * top, y + dy + d * top),
   view.convertToViewportPoint(x + dx + c * bottom, y + dy + d * bottom)
  ]
  parts.push({text: item.str, box: bounds(points)})
 }
 if (parts.map(p => p.text.trim()).join('').length < 25) {
  const ops = await page.getOperatorList()
  if (
   ops.fnArray.some(
    op =>
     op === OPS.paintImageXObject ||
     op === OPS.paintInlineImageXObject ||
     op === OPS.paintImageXObjectRepeat ||
     op === OPS.paintImageMaskXObject ||
     op === OPS.paintImageMaskXObjectGroup ||
     op === OPS.paintImageMaskXObjectRepeat
   )
  ) {
   onOcr()
   const result = await window.assistant.invoke<{lines: {text: string; box: Rect}[]}>('textbook:ocr-page', {
    id: bookId,
    page: page.pageNumber
   })
   if (result.lines.length)
    parts = result.lines.map(({text, box: [l, t, r, b]}) => ({
     text,
     box: [l * view.width, t * view.height, (r - l) * view.width, (b - t) * view.height]
    }))
  }
 }
 const marks: number[][] = (await page.getAnnotations({intent: 'display'}))
  .filter(ann => ann.subtype === 'Highlight' || ann.subtype === 'Underline' || ann.subtype === 'Ink')
  .map(ann => ann.rect)
 return groupFragments(parts).map((row, i) => {
  const rect = pdfRect(row.box, view),
   [x, y, w, h] = rect
  const marked = marks.some(
   ([a, b, c, d]) => Math.min(x + w, c) > Math.max(a, x) && Math.min(y + h, d) > Math.max(b, y)
  )
  return {page: page.pageNumber, line: i + 1, text: row.text.trim(), rect, marked}
 })
}
function inkIntegral(pix: Uint8ClampedArray, width: number, height: number) {
 const stride = width + 1
 const ink = new Uint32Array(stride * (height + 1))
 for (let y = 0; y < height; y++) {
  let sum = 0
  for (let x = 0; x < width; x++) {
   const i = (y * width + x) * 4
   if (Math.min(pix[i], pix[i + 1], pix[i + 2]) < 200) sum++
   ink[(y + 1) * stride + x + 1] = ink[y * stride + x + 1] + sum
  }
 }
 return {ink, stride}
}

function hasInk(ink: Uint32Array, stride: number, left: number, top: number, right: number, bottom: number) {
 return (
  ink[bottom * stride + right] - ink[top * stride + right] - ink[bottom * stride + left] + ink[top * stride + left] > 2
 )
}

function overlappingMark(occupied: Rect[], left: number, top: number, right: number, bottom: number) {
 return occupied.some(([x, y, w, h]) => x < right && x + w > left && y < bottom && y + h > top)
}

type Placement = {
 x: number
 y: number
 tw: number
 th: number
 left: number
 top: number
 width: number
 height: number
 center: number[]
 viewWidth: number
}

function placementDistance(box: Placement) {
 const {x, y, tw, th, left, width, height, center, viewWidth} = box
 const lateral = x >= left + width || x + tw <= left
 const aligned = center[1] >= y - 5 && center[1] <= y + th + 5
 const gap = Math.max(0, x - left - width, left - x - tw)
 return (
  Math.hypot(x + tw / 2 - center[0], (y + th / 2 - center[1]) * 1.8) +
  (lateral && aligned && gap < viewWidth * 0.2 ? 0 : viewWidth)
 )
}

export async function summarySpace(page: PDFPageProxy, lines: ChapterLine[], title: string) {
 const heading = headingLine(lines, title)
 if (!heading) throw new Error('没有在章首页找到这个章号或标题，请核对正文首页和章名。')
 const base = page.getViewport({scale: 1})
 const scale = Math.min(1, 1000 / Math.max(base.width, base.height))
 const view = page.getViewport({scale})
 const canvas = document.createElement('canvas')
 canvas.width = Math.ceil(view.width)
 canvas.height = Math.ceil(view.height)
 const ctx = canvas.getContext('2d', {willReadFrequently: true})
 if (!ctx) throw new Error('无法检查章号旁的留白')
 // Suppress generated annotations when looking for space to refresh an existing note.
 await page.render({canvas, canvasContext: ctx, viewport: view, annotationMode: 0}).promise
 const pix = ctx.getImageData(0, 0, canvas.width, canvas.height).data
 const [left, top, width, height] = visualRect(heading.rect, view)
 const center = [left + width / 2, top + height / 2]
 const tw = Math.max(1, Math.floor(Math.min(190 * scale, view.width * 0.45))),
  th = Math.max(1, Math.ceil(65 * scale)),
  margin = 5
 // Check all pixels in a candidate. This also protects diagrams and scan ink not in the text layer.
 const {ink, stride} = inkIntegral(pix, canvas.width, canvas.height)
 let best: Rect | undefined,
  distance = Infinity
 const occupied: Rect[] = (await page.getAnnotations({intent: 'display'}))
  .filter(
   ann =>
    ann.rect &&
    !(ann.subtype === 'FreeText' && ann.titleObj?.str === 'Zynote' && (ann.contentsObj?.str ?? '').includes(title))
  )
  .map(ann => visualRect([ann.rect[0], ann.rect[1], ann.rect[2] - ann.rect[0], ann.rect[3] - ann.rect[1]], view))
 const extra = 4
 for (
  let y = Math.max(margin, Math.floor(top - th - 64));
  y <= Math.min(canvas.height - th - margin, Math.ceil(top + height + 64));
  y += 5
 ) {
  for (let x = margin; x + tw + margin < canvas.width; x += 5) {
   const l = x - extra,
    t = y - extra,
    r = x + tw + extra,
    b = y + th + extra
   if (hasInk(ink, stride, l, t, r, b)) continue
   if (overlappingMark(occupied, l, t, r, b)) continue
   const d = placementDistance({x, y, tw, th, left, top, width, height, center, viewWidth: view.width})
   if (d < distance) {
    best = [x, y, tw, th]
    distance = d
   }
  }
 }
 canvas.width = 0
 if (!best) throw new Error('章号附近没有足够放下小结的空白，已保留原页。')
 return pdfRect(best, view)
}
