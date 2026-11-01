import {afterEach, expect, it, vi} from 'vitest'
import {TextbookPreview} from './textbook-protocol'

const path = '/textbooks/11111111-1111-4111-8111-111111111111'
const local = 'http://127.0.0.1:32101' + path
const preview = 'textbook-pdf://preview' + path
const dev = 'http://localhost:5173'
afterEach(() => vi.unstubAllGlobals())

it('forwards only a textbook range to the port issued by Go', async () => {
 const fetch = vi.fn().mockResolvedValue(
  new Response('abc', {
   status: 206,
   headers: {
    'Content-Range': 'bytes 3-5/6',
    'Content-Length': '3',
    'Accept-Ranges': 'bytes',
    'Set-Cookie': 'unused=example'
   }
  })
 )
 vi.stubGlobal('fetch', fetch)
 const books = new TextbookPreview(dev)
 expect(books.publish(local)).toBe(preview)
 const request = new Request(preview, {headers: {Origin: dev, Range: 'bytes=3-5', Cookie: 'unused=example'}})
 const result = await books.serve(request)
 expect(fetch).toHaveBeenCalledWith(local, {
  method: 'GET',
  headers: {Range: 'bytes=3-5'},
  signal: request.signal,
  credentials: 'omit',
  redirect: 'error'
 })
 expect(result.status).toBe(206)
 expect(result.headers.get('access-control-allow-origin')).toBe(dev)
 expect(result.headers.get('content-range')).toBe('bytes 3-5/6')
 expect(result.headers.get('set-cookie')).toBeNull()
 expect(await result.text()).toBe('abc')
})

it('rejects untrusted sources, paths, methods and origins before contacting Go', async () => {
 const fetch = vi.fn()
 vi.stubGlobal('fetch', fetch)
 const books = new TextbookPreview()
 for (const endpoint of [
  null,
  'http://localhost:32101' + path,
  'https://127.0.0.1:32101' + path,
  local + '?x',
  'http://127.0.0.1:0' + path,
  'http://user@127.0.0.1:32101' + path,
  local + '/extra'
 ])
  expect(() => books.publish(endpoint)).toThrow()
 books.publish(local)
 expect((await books.serve(new Request(preview, {headers: {Origin: dev}}))).status).toBe(403)
 expect((await books.serve(new Request(preview + '?next=1'))).status).toBe(404)
 expect((await books.serve(new Request(preview.replace('preview', 'elsewhere')))).status).toBe(404)
 expect((await books.serve(new Request(preview, {method: 'POST'}))).status).toBe(405)
 expect((await books.serve(new Request(preview, {headers: {Range: 'bytes=0-1,7-8'}}))).status).toBe(416)
 books.clear()
 expect((await books.serve(new Request(preview))).status).toBe(404)
 expect(fetch).not.toHaveBeenCalled()
})

it('supports file-renderer HEAD requests and preserves unsatisfied ranges', async () => {
 const fetch = vi
  .fn()
  .mockResolvedValueOnce(new Response(null, {headers: {'Content-Length': '1200'}}))
  .mockResolvedValueOnce(new Response(null, {status: 416, headers: {'Content-Range': 'bytes */1200'}}))
 vi.stubGlobal('fetch', fetch)
 const books = new TextbookPreview()
 books.publish(local)
 const metadata = await books.serve(new Request(preview, {method: 'HEAD', headers: {Origin: 'null'}}))
 expect(metadata.headers.get('content-length')).toBe('1200')
 expect(metadata.headers.get('access-control-allow-origin')).toBe('null')
 expect(metadata.body).toBeNull()
 const invalid = await books.serve(new Request(preview, {headers: {Range: 'bytes=1300-'}}))
 expect(invalid.status).toBe(416)
 expect(invalid.headers.get('content-range')).toBe('bytes */1200')
})
