import {useCallback, useLayoutEffect, useRef, useState} from 'react'
import {usePdfPage} from '@renderer/features/study/usePdfPage'
import type {Book} from '@renderer/features/study/types'

type ZoomAnchor = {x: number; y: number; fractionX: number; fractionY: number; zoom: number}

function wheelUnit(deltaMode: number, pageHeight: number) {
 if (deltaMode === 1) return 16
 if (deltaMode === 2) return pageHeight
 return 1
}

export function usePdfNavigation(book: Book | null, pageNumber: number, reading: boolean, version: number) {
 const frame = useRef<HTMLDivElement>(null)
 const paper = useRef<HTMLDivElement>(null)
 const [width, setWidth] = useState(0)
 const [zoom, setZoom] = useState(1)
 const requested = useRef(1)
 const animation = useRef(0)
 const anchor = useRef<ZoomAnchor | null>(null)
 const pdf = usePdfPage(book, pageNumber, width, zoom, version)

 useLayoutEffect(() => {
  const element = frame.current
  if (!element) return
  const observer = new ResizeObserver(([box]) => {
   if (box.contentRect.width > 0) setWidth(Math.floor(box.contentRect.width))
  })
  observer.observe(element)
  return () => observer.disconnect()
 }, [book?.id])

 const zoomTo = useCallback((value: number, clientX?: number, clientY?: number) => {
  const scroll = frame.current
  const sheet = paper.current
  const next = Math.max(0.5, Math.min(2, value))
  if (!scroll || !sheet || next === requested.current) return
  const box = sheet.getBoundingClientRect()
  const area = scroll.getBoundingClientRect()
  const x = clientX ?? area.left + scroll.clientLeft + scroll.clientWidth / 2
  const y = clientY ?? area.top + scroll.clientTop + scroll.clientHeight / 2
  if (box.width > 1 && box.height > 1) {
   // Wheel events can arrive between an intermediate layout and its scroll correction.
   // Carry the paper point forward, adjusting only for actual movement of the pointer.
   const prior = anchor.current
   const fractionX = prior ? prior.fractionX + (x - prior.x) / box.width : (x - box.left) / box.width
   const fractionY = prior ? prior.fractionY + (y - prior.y) / box.height : (y - box.top) / box.height
   anchor.current = {x, y, fractionX, fractionY, zoom: next}
  }
  requested.current = next
  if (!animation.current) {
   animation.current = requestAnimationFrame(() => {
    animation.current = 0
    setZoom(requested.current)
   })
  }
 }, [])

 useLayoutEffect(() => {
  // A previous page's pending render must not move the newly opened page.
  anchor.current = null
  requested.current = zoom
  return () => {
   cancelAnimationFrame(animation.current)
   animation.current = 0
   anchor.current = null
  }
 }, [book?.id, pageNumber, version])

 useLayoutEffect(() => {
  const point = anchor.current
  const element = frame.current
  const box = paper.current?.getBoundingClientRect()
  if (!point || !element || !box || pdf.appliedZoom === undefined) return
  element.scrollLeft += box.left + point.fractionX * box.width - point.x
  element.scrollTop += box.top + point.fractionY * box.height - point.y
  if (point.zoom === pdf.appliedZoom && point.zoom === requested.current) anchor.current = null
 }, [pdf.viewport, pdf.appliedZoom])

 useLayoutEffect(() => {
  const element = frame.current
  if (!element) return
  const wheel = (event: WheelEvent) => {
   if (!event.ctrlKey && !event.metaKey) return
   event.preventDefault()
   const unit = wheelUnit(event.deltaMode, element.clientHeight)
   const distance = Math.max(-120, Math.min(120, event.deltaY * unit))
   zoomTo(requested.current * 1.1 ** (-distance / 100), event.clientX, event.clientY)
  }
  element.addEventListener('wheel', wheel, {passive: false})
  return () => element.removeEventListener('wheel', wheel)
 }, [book?.id, zoomTo])

 useLayoutEffect(() => {
  const sheet = paper.current
  const scroll = frame.current
  if (!reading || !sheet || !scroll) return
  let drag: {pointer: number; x: number; y: number; left: number; top: number} | undefined
  const down = (event: PointerEvent) => {
   if (!event.isPrimary || event.button !== 0 || event.pointerType !== 'mouse') return
   if (event.target instanceof Element && event.target.closest('button, a, input, textarea, [data-pdf-interactive]'))
    return
   event.preventDefault()
   sheet.focus({preventScroll: true})
   sheet.setPointerCapture(event.pointerId)
   sheet.dataset.panning = 'true'
   drag = {pointer: event.pointerId, x: event.clientX, y: event.clientY, left: scroll.scrollLeft, top: scroll.scrollTop}
  }
  const move = (event: PointerEvent) => {
   if (drag?.pointer !== event.pointerId) return
   scroll.scrollLeft = drag.left + drag.x - event.clientX
   scroll.scrollTop = drag.top + drag.y - event.clientY
  }
  const end = () => {
   if (drag && sheet.hasPointerCapture(drag.pointer)) sheet.releasePointerCapture(drag.pointer)
   drag = undefined
   delete sheet.dataset.panning
  }
  sheet.addEventListener('pointerdown', down)
  sheet.addEventListener('pointermove', move)
  sheet.addEventListener('pointerup', end)
  sheet.addEventListener('pointercancel', end)
  sheet.addEventListener('lostpointercapture', end)
  return () => {
   end()
   sheet.removeEventListener('pointerdown', down)
   sheet.removeEventListener('pointermove', move)
   sheet.removeEventListener('pointerup', end)
   sheet.removeEventListener('pointercancel', end)
   sheet.removeEventListener('lostpointercapture', end)
  }
 }, [book?.id, pageNumber, reading])

 return {
  frame,
  paper,
  zoom,
  zoomTo,
  zoomBy: (step: number) => zoomTo(Math.round((requested.current + step) * 100) / 100),
  ...pdf
 }
}
