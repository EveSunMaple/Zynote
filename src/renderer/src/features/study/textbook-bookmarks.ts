import type {PDFDocumentProxy} from 'pdfjs-dist/legacy/build/pdf.mjs'

type Destination = string | unknown[]
type OutlineItem = {title: string; dest: Destination | null; items: OutlineItem[]}
export type TextbookBookmark = {label: string; dest: Destination; parent: string}

export function textbookBookmarks(outline: OutlineItem[] | null): TextbookBookmark[] {
 const rows: TextbookBookmark[] = []
 function visit(items: OutlineItem[], parent: string) {
  for (const item of items) {
   const label = item.title.trim()
   if (label && item.dest) rows.push({label, dest: item.dest, parent})
   visit(item.items, label || parent)
  }
 }
 visit(outline || [], '')
 return rows
}

export function firstBodyBookmark(rows: TextbookBookmark[]) {
 const normalized = (title: string) => title.normalize('NFKC').trim()
 return (
  rows.find(({label}) => /^(?:第\s*(?:一|0*1)\s*章|chapter\s+(?:0*1|i|one)\b)/i.test(normalized(label))) ||
  rows.find(({label}) => /^(?:绪论|绪言|导论|引言|introduction)(?:$|[\s:：—-])/i.test(normalized(label)))
 )
}

export async function bookmarkPage(
 document: Pick<PDFDocumentProxy, 'numPages' | 'getDestination' | 'getPageIndex'>,
 dest: Destination
) {
 // Named destinations and indirect page references are resolved only when the reader chooses a bookmark.
 const explicit = typeof dest === 'string' ? await document.getDestination(dest) : dest
 const ref: unknown = explicit?.[0]
 let index: number
 if (typeof ref === 'number') index = ref
 else if (
  ref &&
  typeof ref === 'object' &&
  'num' in ref &&
  typeof ref.num === 'number' &&
  'gen' in ref &&
  typeof ref.gen === 'number'
 )
  index = await document.getPageIndex({num: ref.num, gen: ref.gen})
 else return null
 return Number.isInteger(index) && index >= 0 && index < document.numPages ? index + 1 : null
}
