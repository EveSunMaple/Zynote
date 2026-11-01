import {afterEach, expect, it, vi} from 'vitest'
import {getDocument} from 'pdfjs-dist/legacy/build/pdf.mjs'
import type {DocumentInitParameters, PDFDocumentLoadingTask} from 'pdfjs-dist/types/src/display/api'
import {openStudyPdf} from './pdf-document'

vi.mock('pdfjs-dist/legacy/build/pdf.mjs', () => ({
 GlobalWorkerOptions: {},
 getDocument: vi.fn(),
 PDFDataRangeTransport: class {
  onDataRange = vi.fn()
 }
}))

const url = 'textbook-pdf://preview/textbooks/test'
const chunk = 1024 * 1024
afterEach(() => {
 vi.unstubAllGlobals()
 vi.clearAllMocks()
})

function setup() {
 const destroy = vi.fn().mockResolvedValue(undefined)
 vi.mocked(getDocument).mockReturnValue({promise: Promise.resolve({}), destroy} as unknown as PDFDocumentLoadingTask)
 const fetchRange = vi.fn()
 vi.stubGlobal('fetch', fetchRange)
 vi.stubGlobal('document', {baseURI: 'file:///app/index.html'})
 return {fetchRange, destroy, controller: new AbortController(), fail: vi.fn()}
}

it('reads large books through bounded byte ranges and reports errors after opening', async () => {
 const {fetchRange, destroy, controller, fail} = setup()
 const length = 5 * chunk
 fetchRange.mockResolvedValueOnce(
  new Response(new Uint8Array(chunk), {status: 206, headers: {'Content-Range': `bytes 0-${chunk - 1}/${length}`}})
 )
 await openStudyPdf(url, controller.signal, fail)
 expect(fetchRange).toHaveBeenCalledWith(url, expect.objectContaining({headers: {Range: `bytes=0-${chunk - 1}`}}))
 const source = vi.mocked(getDocument).mock.calls[0][0] as DocumentInitParameters
 expect(source).toMatchObject({disableAutoFetch: true, disableStream: true, rangeChunkSize: chunk})
 const range = source.range!
 fetchRange.mockResolvedValueOnce(
  new Response('end', {status: 206, headers: {'Content-Range': `bytes ${length - 3}-${length - 1}/${length}`}})
 )
 range.requestDataRange(length - 3, length)
 await vi.waitFor(() => expect(range.onDataRange).toHaveBeenCalledWith(length - 3, new TextEncoder().encode('end')))
 expect(fetchRange).toHaveBeenLastCalledWith(
  url,
  expect.objectContaining({headers: {Range: `bytes=${length - 3}-${length - 1}`}})
 )
 fetchRange.mockResolvedValueOnce(new Response(null, {status: 416}))
 range.requestDataRange(chunk, 2 * chunk)
 await vi.waitFor(() =>
  expect(fail).toHaveBeenCalledWith(expect.objectContaining({message: expect.stringContaining('416')}))
 )
 controller.abort()
 expect(destroy).toHaveBeenCalledOnce()
})

it('refuses an ignored range on a large document before reading response bytes', async () => {
 const {fetchRange, controller, fail} = setup()
 const response = new Response('irrelevant', {headers: {'Content-Length': String(3 * chunk)}})
 const read = vi.spyOn(response, 'arrayBuffer')
 fetchRange.mockResolvedValueOnce(response)
 await expect(openStudyPdf(url, controller.signal, fail)).rejects.toThrow('教材分块读取失败')
 expect(read).not.toHaveBeenCalled()
 expect(getDocument).not.toHaveBeenCalled()
})
