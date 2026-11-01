import {useEffect, useRef, useState} from 'react'
import type {PDFDocumentProxy, PDFPageProxy, PageViewport} from 'pdfjs-dist/legacy/build/pdf.mjs'
import {openStudyPdf} from '@renderer/features/study/pdf-document'
import type {Book} from '@renderer/features/study/types'

const invoke = <T>(action: string, args?: unknown) => window.assistant.invoke<T>(action, args)

export function usePdfPage(book: Book | null, pageNumber: number, frameWidth: number, zoom: number, version = 0) {
 const canvas = useRef<HTMLCanvasElement | null>(null),
  [opened, setOpened] = useState<{document: PDFDocumentProxy; id: string; version: number} | null>(null),
  [renderedPage, setRenderedPage] = useState<PDFPageProxy | null>(null),
  [layout, setLayout] = useState<{viewport: PageViewport; zoom: number; page: number; book: string} | null>(null),
  [opening, setOpening] = useState(false),
  [rendering, setRendering] = useState(false),
  [error, setError] = useState('')

 const document = opened?.id === book?.id && opened?.version === version ? opened.document : null
 useEffect(() => {
  setOpened(null)
  setRenderedPage(null)
  setLayout(null)
  setError('')
  setOpening(!!book)
  setRendering(false)
  const target = canvas.current
  if (target) target.getContext('2d')?.clearRect(0, 0, target.width, target.height)
  if (!book) {
   return
  }
  const controller = new AbortController()
  const fail = (reason: unknown) => {
   if (controller.signal.aborted) return
   // PDF.js range requests can also fail after the document has opened.
   controller.abort()
   setOpened(null)
   setOpening(false)
   setRendering(false)
   setError(reason instanceof Error ? reason.message : String(reason))
  }
  void invoke<string>('textbook:bytes', {id: book.id})
   .then(url => openStudyPdf(url, controller.signal, fail))
   .then(value => {
    if (!controller.signal.aborted) {
     setOpening(false)
     setOpened({document: value, id: book.id, version})
     setError('')
    }
   })
   .catch(fail)
  return () => controller.abort()
 }, [book?.id, version])

 useEffect(() => {
  if (!document || !canvas.current || frameWidth <= 1) return
  setRenderedPage(null)
  let active = true,
   page: PDFPageProxy | null = null,
   renderTask: ReturnType<PDFPageProxy['render']> | null = null
  setRendering(true)
  const task = (async () => {
   page = await document.getPage(pageNumber)
   if (!active) return
   const base = page.getViewport({scale: 1}),
    scale = (frameWidth * zoom) / base.width,
    next = page.getViewport({scale}),
    target = canvas.current
   if (!target || !book || !active) return
   // Commit the page geometry before a slow rasterization, so zoom anchors and overlays use the same viewport.
   setLayout({viewport: next, zoom, page: pageNumber, book: book.id})
   const ratio = Math.min(
    2,
    window.devicePixelRatio || 1,
    Math.sqrt(8000000 / (next.width * next.height)),
    32767 / Math.max(next.width, next.height)
   )
   target.width = Math.max(1, Math.floor(next.width * ratio))
   target.height = Math.max(1, Math.floor(next.height * ratio))
   const context = target.getContext('2d')
   if (!context) throw new Error('无法创建教材画布')
   renderTask = page.render({
    canvas: target,
    canvasContext: context,
    viewport: next,
    transform: ratio === 1 ? undefined : [ratio, 0, 0, ratio, 0, 0]
   })
   await renderTask.promise
   if (active) {
    setRenderedPage(page)
    setError('')
   }
  })()
  void task
   .catch(reason => {
    if (active && (reason as {name?: string})?.name !== 'RenderingCancelledException')
     setError(reason instanceof Error ? reason.message : String(reason))
   })
   .finally(() => {
    if (active) setRendering(false)
    page?.cleanup()
   })
  return () => {
   active = false
   renderTask?.cancel()
   page?.cleanup()
  }
 }, [document, book?.id, pageNumber, frameWidth, zoom])

 const activeLayout = layout?.book === book?.id && layout?.page === pageNumber ? layout : null
 const viewport = activeLayout?.viewport ?? null
 const size = {width: viewport?.width ?? 1, height: viewport?.height ?? 1}
 return {
  canvas,
  document,
  viewport,
  appliedZoom: activeLayout?.zoom,
  renderedPage,
  size,
  rendering: opening || rendering,
  error
 }
}
