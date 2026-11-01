import {useEffect, useLayoutEffect, useRef, useState} from 'react'
import {createPortal} from 'react-dom'
import {Button, TextArea} from '@heroui/react'
import type {PageViewport} from 'pdfjs-dist/legacy/build/pdf.mjs'
import {type Highlight, highlightLabels} from '@shared/study'
import {errorMessage} from '@renderer/ui/ToastHost'
import '@renderer/features/study/textbook-comments.css'

const labels = {teacher: '老师讲解 · 智云原文', ai: highlightLabels.ai, personal: '我的笔记'}
type Comment = NonNullable<Highlight['comments']>[number]

export function usePageAnnotations(bookId: string | undefined, page: number, version: number) {
 const [data, setData] = useState<{bookId: string; page: number; rows: Highlight[]} | null>(null)
 const [error, setError] = useState('')
 useEffect(() => {
  let active = true
  setError('')
  if (bookId)
   void window.assistant
    .invoke<Highlight[]>('textbook:annotations', {id: bookId, page})
    .then(rows => {
     if (active) setData({bookId, page, rows})
    })
    .catch(reason => {
     if (active) {
      setError(errorMessage(reason))
      setData(null)
     }
    })
  return () => {
   active = false
  }
 }, [bookId, page, version])
 return {rows: data?.bookId === bookId && data?.page === page ? data.rows : [], error}
}

export function CommentText({comment}: Readonly<{comment: Comment}>) {
 return (
  <div className="book-comment__item" data-study-source={comment.kind}>
   <strong>
    {labels[comment.kind]} {comment.timestamp}
   </strong>
   <p>{comment.text}</p>
   {comment.quote && comment.quote !== comment.text && <small>课堂原话 · {comment.quote}</small>}
  </div>
 )
}

function bounds(row: Highlight, view: PageViewport) {
 const [x, y, w, h] = row.rect
 const [x1, y1] = view.convertToViewportPoint(x, y)
 const [x2, y2] = view.convertToViewportPoint(x + w, y + h)
 return {left: Math.min(x1, x2), top: Math.min(y1, y2), width: Math.abs(x2 - x1), height: Math.abs(y2 - y1)}
}

type Selected = {ids: string[]; anchor: HTMLButtonElement; pinned: boolean}

// A passage may have separately saved marks. Resolve the same stack for hover, click and locate.
function marksAt(anchor: HTMLButtonElement, primaryId: string, x?: number, y?: number) {
 const box = anchor.getBoundingClientRect()
 const hits = document.elementsFromPoint(x ?? box.left + box.width / 2, y ?? box.top + box.height / 2)
 const ids = new Set([primaryId])
 for (const el of hits) {
  const id = el.closest<HTMLElement>('[data-book-mark]')?.dataset.bookMark
  if (id) ids.add(id)
 }
 return [...ids]
}

export function TextbookComments({
 rows,
 viewport,
 bookId,
 locateId,
 onLocated,
 onSaved
}: Readonly<{
 rows: Highlight[]
 viewport: PageViewport
 bookId: string
 locateId?: string
 onLocated: () => void
 onSaved: () => void
}>) {
 const [selected, setSelected] = useState<Selected | null>(null)
 const [note, setNote] = useState('')
 const [editing, setEditing] = useState<{id: string; previousText: string} | null>(null)
 const [pending, setPending] = useState(false)
 const [error, setError] = useState('')
 const card = useRef<HTMLDivElement>(null)
 const destination = useRef<HTMLButtonElement>(null)
 const hideTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
 const selectedRows = selected?.ids.flatMap(id => rows.filter(row => row.id === id)) || []
 const current = selectedRows[0]
 useLayoutEffect(() => {
  const anchor = destination.current
  if (!anchor || !locateId) return
  anchor.scrollIntoView({block: 'center', inline: 'nearest'})
  anchor.focus({preventScroll: true})
  setSelected({ids: marksAt(anchor, locateId), anchor, pinned: true})
  onLocated()
 }, [locateId, rows, viewport, onLocated])
 function close() {
  setSelected(null)
  setEditing(null)
  setNote('')
  setError('')
 }
 function keep() {
  clearTimeout(hideTimer.current)
 }
 function leave() {
  keep()
  if (!selected?.pinned)
   hideTimer.current = setTimeout(() => setSelected(previous => (previous?.pinned ? previous : null)), 180)
 }
 useEffect(() => () => clearTimeout(hideTimer.current), [])
 useEffect(() => {
  const node = card.current
  if (!node) return
  const cancel = () => clearTimeout(hideTimer.current)
  const dismiss = () => {
   cancel()
   if (!selected?.pinned)
    hideTimer.current = setTimeout(() => setSelected(previous => (previous?.pinned ? previous : null)), 180)
  }
  const escape = (event: KeyboardEvent) => {
   if (event.key !== 'Escape' || pending) return
   setSelected(null)
   setEditing(null)
   setNote('')
   setError('')
  }
  node.addEventListener('pointerenter', cancel)
  node.addEventListener('pointerleave', dismiss)
  node.addEventListener('keydown', escape)
  return () => {
   node.removeEventListener('pointerenter', cancel)
   node.removeEventListener('pointerleave', dismiss)
   node.removeEventListener('keydown', escape)
  }
 }, [selected, pending])
 useLayoutEffect(() => {
  const box = card.current
  if (!selected || !box) return
  const anchor = selected.anchor
  const frame = anchor.closest<HTMLElement>('.study-canvas')
  if (!frame) return
  const toolbar = frame.parentElement?.querySelector('.study-canvas-toolbar')
  function place() {
   if (!box || !frame || !anchor.isConnected) return
   // The sticky toolbar can cover the top of a partially scrolled PDF viewport.
   const gap = 6
   const canvas = frame.getBoundingClientRect()
   const left = Math.max(0, canvas.left + frame.clientLeft) + gap
   const right = Math.min(window.innerWidth, canvas.left + frame.clientLeft + frame.clientWidth) - gap
   const top = Math.max(0, canvas.top + frame.clientTop, toolbar?.getBoundingClientRect().bottom ?? 0) + gap
   const bottom = Math.min(window.innerHeight, canvas.top + frame.clientTop + frame.clientHeight) - gap
   const rect = anchor.getBoundingClientRect()
   const above = rect.top - gap - top,
    below = bottom - rect.bottom - gap
   const offscreen = rect.bottom <= top || rect.top >= bottom || rect.right <= left || rect.left >= right
   if (offscreen || right - left < 180 || Math.max(above, below) < 144) {
    box.dataset.docked = 'true'
    return
   }
   // Keep enough room to read and scroll the card without covering its source text.
   delete box.dataset.docked
   box.style.setProperty('--comment-width', right - left + 'px')
   const useBelow = below >= Math.min(400, box.scrollHeight + 2) || below >= above
   box.style.setProperty('--comment-height', (useBelow ? below : above) + 'px')
   box.style.left = Math.max(left, Math.min(rect.left, right - box.offsetWidth)) + 'px'
   box.style.top = (useBelow ? rect.bottom + gap : rect.top - gap - box.offsetHeight) + 'px'
  }
  place()
  const observer = new ResizeObserver(place)
  observer.observe(box)
  observer.observe(frame)
  observer.observe(anchor)
  window.addEventListener('scroll', place, true)
  window.addEventListener('resize', place)
  return () => {
   observer.disconnect()
   window.removeEventListener('scroll', place, true)
   window.removeEventListener('resize', place)
  }
 }, [selected, current?.id])
 function open(anchor: HTMLButtonElement, row: Highlight, pinned: boolean, x?: number, y?: number) {
  keep()
  if (selected?.pinned && (!pinned || note.trim() || pending)) return
  const ids = marksAt(anchor, row.id, x, y)
  if (selected?.anchor === anchor && selected.pinned === pinned && ids.join() === selected.ids.join()) return
  setSelected({ids, anchor, pinned})
  setError('')
  setEditing(null)
  setNote('')
 }
 async function save() {
  if (!current || !note.trim() || pending) return
  setPending(true)
  setError('')
  try {
   await window.assistant.invoke('textbook:comment', {
    id: bookId,
    markId: editing?.id ?? current.id,
    text: note.trim(),
    previousText: editing?.previousText
   })
   setNote('')
   setEditing(null)
   onSaved()
  } catch (reason) {
   setError(errorMessage(reason))
  } finally {
   setPending(false)
  }
 }
 const comments = selectedRows.flatMap(row => (row.comments || []).map(c => ({c, id: row.id})))
 const seen = new Set<string>()
 return (
  <>
   {rows
    .filter(row => !row.chapter)
    .map(row => {
     const box = bounds(row, viewport)
     return (
      <button
       type="button"
       key={row.id}
       ref={row.id === locateId ? destination : undefined}
       data-book-mark={row.id}
       data-study-source={row.source}
       aria-label={'查看批注：' + row.quote}
       className={'book-comment__anchor' + (row.ink ? ' book-comment__ink' : '')}
       style={box}
       onPointerEnter={e => open(e.currentTarget, row, false, e.clientX, e.clientY)}
       onPointerMove={e => open(e.currentTarget, row, false, e.clientX, e.clientY)}
       onPointerLeave={leave}
       onFocus={e => open(e.currentTarget, row, false)}
       onBlur={leave}
       onClick={e =>
        open(e.currentTarget, row, true, e.detail ? e.clientX : undefined, e.detail ? e.clientY : undefined)
       }
      >
       {row.ink && (
        <svg width="100%" height="100%" aria-hidden="true">
         <polyline
          points={row.ink
           .map(([x, y]) => {
            const [px, py] = viewport.convertToViewportPoint(x, y)
            return [px - box.left, py - box.top].join(',')
           })
           .join(' ')}
         />
        </svg>
       )}
      </button>
     )
    })}
   {selected &&
    current &&
    createPortal(
     <section ref={card} className="book-comment" aria-label="划线批注">
      <div className="book-comment__head">
       <strong>划线批注</strong>
       {selected.pinned && (
        <Button variant="ghost" size="sm" isDisabled={pending} onPress={close}>
         关闭批注
        </Button>
       )}
      </div>
      {comments.map(({c, id}) => {
       const key = JSON.stringify([c.kind, c.text, c.quote, c.timestamp])
       if (seen.has(key)) return null
       seen.add(key)
       return (
        <div key={key}>
         <CommentText comment={c} />
         {selected.pinned && c.kind === 'personal' && (
          <Button
           size="sm"
           variant="ghost"
           isDisabled={pending}
           onPress={() => {
            setEditing({id, previousText: c.text})
            setNote(c.text)
           }}
          >
           编辑我的笔记
          </Button>
         )}
        </div>
       )
      })}
      {selectedRows
       .filter(row => !row.comments?.length && row.evidence && row.evidence !== '手动标注')
       .map(row => (
        <div key={row.id} className="book-comment__item" data-study-source={row.source}>
         <strong>原划线备注</strong>
         <p>{row.evidence}</p>
        </div>
       ))}
      {selected.pinned ? (
       <div className="book-comment__editor">
        <TextArea
         aria-label="我的批注"
         value={note}
         maxLength={3000}
         placeholder="写下我的笔记…"
         onChange={e => setNote(e.target.value)}
        />
        {error && <small role="alert">{error}</small>}
        <Button size="sm" variant="primary" isDisabled={!note.trim() || pending} onPress={() => void save()}>
         {editing ? '保存修改' : '添加我的笔记'}
        </Button>
       </div>
      ) : (
       <small>点一下划线，可补写自己的笔记。</small>
      )}
     </section>,
     selected.anchor.closest('.study-canvas-column') || document.body
    )}
  </>
 )
}
