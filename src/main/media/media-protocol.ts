import type {BrowserWindow, Session, OnBeforeRequestListenerDetails, CallbackResponse} from 'electron'
import {ManagedPlayback} from './managed-playback'

export function mediaRange(
 value: string | null,
 size: number
): {start: number; end: number; partial: boolean} | undefined {
 if (value === null) return {start: 0, end: size - 1, partial: false}
 if (value.length > 128) return undefined
 const match = /^bytes=(\d*)-(\d*)$/.exec(value)
 if (!match || (!match[1] && !match[2])) return undefined
 const first = match[1] ? Number(match[1]) : undefined,
  last = match[2] ? Number(match[2]) : undefined
 if ((first !== undefined && !Number.isSafeInteger(first)) || (last !== undefined && !Number.isSafeInteger(last)))
  return undefined
 if (first === undefined)
  return last && last > 0 ? {start: Math.max(0, size - last), end: size - 1, partial: true} : undefined
 if (first >= size || (last !== undefined && first > last)) return undefined
 return {start: first, end: last === undefined ? size - 1 : Math.min(last, size - 1), partial: true}
}
export async function mediaResponse(manager: ManagedPlayback, request: Request): Promise<Response> {
 const match = /^lecture-media:\/\/play\/([A-Za-z0-9_-]{43})$/.exec(request.url)
 const token = match && manager.get(match[1])
 if (!token) return new Response(null, {status: 404})
 if (request.method !== 'GET' && request.method !== 'HEAD')
  return new Response(null, {status: 405, headers: {Allow: 'GET, HEAD'}})
 const range = mediaRange(request.headers.get('range'), token.size)
 const headers: Record<string, string> = {
  'Accept-Ranges': 'bytes',
  'Cache-Control': 'no-store',
  'Content-Type': token.mime,
  'X-Content-Type-Options': 'nosniff'
 }
 if (!range) return new Response(null, {status: 416, headers: {...headers, 'Content-Range': 'bytes */' + token.size}})
 headers['Content-Length'] = String(range.end - range.start + 1)
 if (range.partial) headers['Content-Range'] = `bytes ${range.start}-${range.end}/${token.size}`
 try {
  await manager.preflight(token, request.signal)
  const body = request.method === 'HEAD' ? null : manager.stream(token, range.start, range.end, request.signal)
  return new Response(body, {status: range.partial ? 206 : 200, headers})
 } catch (error) {
  return new Response(null, {status: (error as {code?: string})?.code === 'BUSY' ? 429 : 404})
 }
}

// The owner/frame check must run before protocol.handle. Protocol Request has no owner ID.
// This handler is installed only on the main UI's existing session.
export function installMediaProtocol(
 session: Session,
 owner: BrowserWindow,
 manager: ManagedPlayback,
 rendererOrigin: string | undefined
) {
 let disposed = false
 const contents = owner.webContents
 const beforeRequest = (details: OnBeforeRequestListenerDetails, callback: (response: CallbackResponse) => void) => {
  if (disposed || owner.isDestroyed() || contents.isDestroyed()) {
   callback({cancel: true})
   return
  }
  callback({cancel: details.webContentsId !== contents.id || details.frame !== contents.mainFrame})
 }
 session.webRequest.onBeforeRequest({urls: ['lecture-media://*/*']}, beforeRequest)
 session.protocol.handle('lecture-media', async request => {
  if (disposed || owner.isDestroyed() || contents.isDestroyed()) return new Response(null, {status: 404})
  const origin = request.headers.get('origin')
  if (origin && (rendererOrigin ? origin !== rendererOrigin : origin !== 'null'))
   return new Response(null, {status: 404})
  return await mediaResponse(manager, request)
 })
 const revoke = () => manager.closeAll()
 const revokeNavigation = (_event: unknown, _url: string, _isInPlace: boolean, isMainFrame: boolean) => {
  if (isMainFrame) revoke()
 }
 let cleanup: Promise<void> | undefined
 const remove = () => {
  if (cleanup) return cleanup
  disposed = true
  contents.removeListener('did-start-navigation', revokeNavigation)
  contents.removeListener('render-process-gone', revoke)
  owner.removeListener('closed', closed)
  try {
   session.webRequest.onBeforeRequest(null)
  } catch {}
  try {
   session.protocol.unhandle('lecture-media')
  } catch {}
  cleanup = manager.dispose()
  return cleanup
 }
 const closed = () => {
  void remove().catch(() => {})
 }
 contents.on('did-start-navigation', revokeNavigation)
 contents.on('render-process-gone', revoke)
 owner.once('closed', closed)
 return remove
}
