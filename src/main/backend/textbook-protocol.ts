const previewOrigin = 'textbook-pdf://preview'
const bookPath = /^\/textbooks\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

export class TextbookPreview {
 private port = ''

 constructor(private readonly rendererOrigin?: string) {}

 publish(endpoint: unknown) {
  const url = typeof endpoint === 'string' ? URL.parse(endpoint) : null
  if (
   !url ||
   url.protocol !== 'http:' ||
   url.hostname !== '127.0.0.1' ||
   !Number(url.port) ||
   url.origin + url.pathname !== endpoint ||
   !bookPath.test(url.pathname)
  )
   throw new Error('无效的教材预览地址')
  // Only Go selects the loopback port and issues paths for registered textbooks.
  this.port = url.port
  return previewOrigin + url.pathname
 }

 clear() {
  this.port = ''
 }

 async serve(request: Request) {
  const path = request.url.slice(previewOrigin.length)
  const origin = request.headers.get('origin')
  if (!this.port || !request.url.startsWith(previewOrigin) || !bookPath.test(path))
   return new Response(null, {status: 404})
  if (origin && (this.rendererOrigin ? origin !== this.rendererOrigin : origin !== 'null' && origin !== 'file://'))
   return new Response(null, {status: 403})
  if (request.method !== 'GET' && request.method !== 'HEAD')
   return new Response(null, {status: 405, headers: {Allow: 'GET, HEAD'}})
  const range = request.headers.get('range')
  if (range !== null && (range.length > 128 || !/^bytes=(?:\d+-\d*|-\d+)$/.test(range)))
   return new Response(null, {status: 416})
  try {
   const result = await fetch('http://127.0.0.1:' + this.port + path, {
    method: request.method,
    headers: range ? {Range: range} : {},
    signal: request.signal,
    redirect: 'error',
    credentials: 'omit'
   })
   const headers = new Headers({
    'Access-Control-Allow-Origin': origin ?? this.rendererOrigin ?? 'null',
    'Access-Control-Expose-Headers': 'Accept-Ranges, Content-Length, Content-Range',
    'Cache-Control': 'no-store',
    'Content-Type': 'application/pdf',
    'X-Content-Type-Options': 'nosniff'
   })
   for (const name of ['accept-ranges', 'content-length', 'content-range']) {
    const value = result.headers.get(name)
    if (value !== null) headers.set(name, value)
   }
   return new Response(result.body, {status: result.status, headers})
  } catch {
   return new Response(null, {status: 502})
  }
 }
}
