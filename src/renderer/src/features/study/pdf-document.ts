import {getDocument, GlobalWorkerOptions, PDFDataRangeTransport} from 'pdfjs-dist/legacy/build/pdf.mjs'
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.mjs?url'
import type {DocumentInitParameters} from 'pdfjs-dist/types/src/display/api'

GlobalWorkerOptions.workerSrc = workerUrl

const chunkSize = 1024 * 1024

async function readRange(url: string, begin: number, end: number, signal: AbortSignal, knownSize?: number) {
 const response = await fetch(url, {
  headers: {Range: `bytes=${begin}-${end - 1}`},
  signal: AbortSignal.any([signal, AbortSignal.timeout(30000)])
 })
 const contentRange = response.headers.get('Content-Range')
 const partial = response.status === 206
 const size = Number(partial ? contentRange?.split('/')[1] : response.headers.get('Content-Length'))
 const stop = Math.min(end, size)
 const validRange = partial
  ? contentRange === `bytes ${begin}-${stop - 1}/${size}`
  : response.status === 200 && begin === 0 && size <= chunkSize
 if (!validRange || !Number.isSafeInteger(size) || size <= begin || (knownSize !== undefined && size !== knownSize)) {
  await response.body?.cancel()
  throw new Error(`教材分块读取失败（${response.status}），请重新打开教材`)
 }
 const bytes = new Uint8Array(await response.arrayBuffer())
 if (bytes.byteLength !== stop - begin) throw new Error('教材分块长度不一致，请重新打开教材')
 return {bytes, size}
}

async function pdfSource(
 url: string,
 signal: AbortSignal,
 onReadError: (reason: unknown) => void
): Promise<DocumentInitParameters> {
 const first = await readRange(url, 0, chunkSize, signal)
 if (first.bytes.length === first.size) return {data: first.bytes}
 // PDF.js only enables its built-in HTTP range reader for http(s) URLs.
 const range = new PDFDataRangeTransport(first.size, first.bytes, true)
 range.requestDataRange = (begin, end) => {
  void readRange(url, begin, end, signal, first.size)
   .then(({bytes}) => {
    if (!signal.aborted) range.onDataRange(begin, bytes)
   })
   .catch(onReadError)
 }
 return {range}
}

export async function openStudyPdf(url: string, signal: AbortSignal, onReadError: (reason: unknown) => void) {
 signal.throwIfAborted()
 const source = await pdfSource(url, signal, onReadError)
 signal.throwIfAborted()
 // In production these resources ship alongside out/ inside app.asar.
 const base = import.meta.env.DEV ? import.meta.env.VITE_TEXTBOOK_PDF_ASSETS : '../../node_modules/pdfjs-dist/'
 const assets = new URL(base, document.baseURI)
 const task = getDocument({
  ...source,
  disableAutoFetch: true,
  disableStream: true,
  rangeChunkSize: chunkSize,
  canvasMaxAreaInBytes: 32000000,
  cMapUrl: new URL('cmaps/', assets).href,
  standardFontDataUrl: new URL('standard_fonts/', assets).href,
  wasmUrl: new URL('wasm/', assets).href,
  iccUrl: new URL('iccs/', assets).href,
  // Use PDF.js's bundled JS image decoders under the app's strict script CSP.
  useWasm: false
 })
 signal.addEventListener('abort', () => void task.destroy().catch(() => {}), {once: true})
 return task.promise
}
