import {getDocument, GlobalWorkerOptions} from 'pdfjs-dist/legacy/build/pdf.mjs'
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.mjs?url'
import type {DocumentInitParameters} from 'pdfjs-dist/types/src/display/api'

GlobalWorkerOptions.workerSrc = workerUrl

export function openStudyPdf(source: DocumentInitParameters) {
 // In production these resources ship alongside out/ inside app.asar.
 const base = import.meta.env.DEV ? import.meta.env.VITE_TEXTBOOK_PDF_ASSETS : '../../node_modules/pdfjs-dist/'
 const assets = new URL(base, document.baseURI)
 return getDocument({
  ...source,
  cMapUrl: new URL('cmaps/', assets).href,
  standardFontDataUrl: new URL('standard_fonts/', assets).href,
  wasmUrl: new URL('wasm/', assets).href,
  iccUrl: new URL('iccs/', assets).href,
  // Use PDF.js's bundled JS image decoders under the app's strict script CSP.
  useWasm: false
 })
}
