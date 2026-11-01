import {useEffect, useRef, useState} from 'react'
import {Button} from '@heroui/react'
import type {PDFDocumentProxy} from 'pdfjs-dist/legacy/build/pdf.mjs'
import {SelectField} from '@renderer/ui/SelectField'
import {
 bookmarkPage,
 firstBodyBookmark,
 textbookBookmarks,
 type TextbookBookmark
} from '@renderer/features/study/textbook-bookmarks'
import '@renderer/features/study/textbook-bookmarks.css'

export function TextbookBookmarks({
 document,
 onPageNumber
}: Readonly<{document: PDFDocumentProxy; onPageNumber: (page: number) => void}>) {
 const [rows, setRows] = useState<TextbookBookmark[] | null>(null)
 const [error, setError] = useState('')
 const request = useRef(0)
 useEffect(() => {
  let active = true
  void document
   .getOutline()
   .then(outline => {
    if (active) setRows(textbookBookmarks(outline))
   })
   .catch(() => {
    if (active) setRows([])
   })
  return () => {
   active = false
   request.current++
  }
 }, [document])

 async function go(row: TextbookBookmark) {
  const current = ++request.current
  setError('')
  const page = await bookmarkPage(document, row.dest).catch(() => null)
  if (request.current !== current) return
  if (page) onPageNumber(page)
  else setError('这个书签没有有效页码，可在下方输入 PDF 页码。')
 }
 if (!rows) return null
 if (!rows.length) return <p className="study-menu__hint">这本教材没有可用的章节书签，可在下方输入正文的 PDF 页码。</p>
 const body = firstBodyBookmark(rows)
 return (
  <div className="study-bookmarks">
   {body && (
    <Button size="sm" variant="secondary" onPress={() => void go(body)}>
     前往正文
    </Button>
   )}
   <SelectField
    className="study-bookmarks__select"
    label="跳转教材书签"
    placeholder="按书签跳转…"
    searchable={rows.length > 1}
    value=""
    options={rows.map(({label, parent}, index) => ({label, value: String(index), description: parent}))}
    onChange={value => {
     if (value && rows[Number(value)]) void go(rows[Number(value)])
    }}
   />
   {error && (
    <span role="status" className="study-menu__hint">
     {error}
    </span>
   )}
  </div>
 )
}
