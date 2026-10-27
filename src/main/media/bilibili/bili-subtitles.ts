import {bilibiliUrl} from '@main/media/bilibili/video-policy'
import {parseSrt, toSrt} from '@shared/srt'

type BiliFetch = (url: string, init?: RequestInit) => Promise<Response>
type Page = {cid: number; page: number; part: string; duration: number}
type Track = {lan: string; lan_doc?: string; subtitle_url: string; ai_type?: number}
type Cue = {from: number; to: number; content: string}
export type BiliSubtitle = {title: string; page: number; source: string; warning: string; srt: string; count: number}

async function json(url: string, fetcher: BiliFetch, signal: AbortSignal): Promise<unknown> {
 const response = await fetcher(url, {
  credentials: 'omit',
  redirect: 'error',
  signal: AbortSignal.any([signal, AbortSignal.timeout(20000)]),
  headers: {Referer: 'https://www.bilibili.com/'}
 })
 if (!response.ok) throw new Error(`B 站 ${new URL(url).pathname} 返回 HTTP ${response.status}`)
 const size = Number(response.headers.get('content-length') || 0)
 if (size > 4_000_000) throw new Error('字幕响应过大')
 const raw = await response.text()
 if (raw.length > 4_000_000) throw new Error('字幕响应过大')
 return JSON.parse(raw)
}

function subtitleAddress(value: string): string {
 const url = new URL(value.startsWith('//') ? 'https:' + value : value)
 if (
  url.protocol !== 'https:' ||
  url.username ||
  url.password ||
  (url.port && url.port !== '443') ||
  !/(^|\.)(hdslb\.com|bilibili\.com)$/.test(url.hostname)
 )
  throw new Error('字幕地址不受信任')
 return url.href
}

function checkedCues(value: unknown, duration: number): Cue[] {
 const body = (value as {body?: unknown})?.body
 if (!Array.isArray(body) || !body.length || body.length > 20000) throw new Error('字幕内容为空或格式错误')
 const cues = body
  .map((row: unknown) => {
   const cue = row as Partial<Cue>
   if (
    !cue ||
    typeof cue.from !== 'number' ||
    typeof cue.to !== 'number' ||
    !Number.isFinite(cue.from) ||
    !Number.isFinite(cue.to) ||
    cue.from < 0 ||
    cue.to <= cue.from ||
    typeof cue.content !== 'string'
   )
    throw new Error('字幕时间或文字异常')
   const content = cue.content.replace(/[\x00-\x1f]+/g, ' ').trim()
   if (!content || content.length > 2000) throw new Error('字幕时间或文字异常')
   return {from: cue.from, to: cue.to, content}
  })
  .sort((a, b) => a.from - b.from)
 if (duration > 0 && cues.at(-1)!.to > duration * 1.25 + 30) throw new Error('字幕时间超出视频，可能属于其他视频')
 return cues
}

function biliVideoID(url: URL) {
 return url.pathname.split('/').findLast(Boolean)!
}
function biliPageListURL(videoId: string) {
 const value = new URL('https://api.bilibili.com/x/player/pagelist')
 value.searchParams.set(
  videoId.startsWith('BV') ? 'bvid' : 'aid',
  videoId.startsWith('BV') ? videoId : videoId.slice(2)
 )
 return value
}
function preferredSubtitleTracks(tracks: Track[]) {
 const chinese = (track: Track) => /^(zh|ai-zh)/i.test(track.lan || '')
 const automatic = (track: Track) => /^(ai-|auto)/i.test(track.lan || '') || !!track.ai_type
 return [...tracks].sort(
  (a, b) => Number(!chinese(a)) - Number(!chinese(b)) || Number(automatic(a)) - Number(automatic(b))
 )
}
function subtitleTrackResult(track: Track, page: Page, pageNumber: number, body: unknown) {
 const cues = checkedCues(body, page.duration)
 const segments = cues
  .map((cue, i) => ({
   start: Math.round(cue.from * 1000),
   end: Math.round(Math.min(cue.to, i + 1 < cues.length ? cues[i + 1].from : cue.to) * 1000),
   text: cue.content
  }))
  .filter(c => c.end > c.start)
 const srt = toSrt(segments)
 parseSrt(srt)
 return {
  title: page.part,
  page: pageNumber,
  source: track.lan_doc || track.lan,
  warning:
   /^(ai-|auto)/i.test(track.lan || '') || track.ai_type ? '自动字幕可能有错，请核对后再作为课堂原文使用。' : '',
  srt,
  count: segments.length
 }
}
export async function readBiliSubtitles(input: string, fetcher: BiliFetch, signal: AbortSignal): Promise<BiliSubtitle> {
 const url = new URL(bilibiliUrl(input)),
  videoId = biliVideoID(url)
 const pages = (await json(biliPageListURL(videoId).href, fetcher, signal)) as {code?: number; data?: Page[]}
 if (pages.code !== 0 || !Array.isArray(pages.data)) throw new Error('无法读取 B 站视频分集')
 const pageNumber = Number(url.searchParams.get('p') || 1),
  page = pages.data.find(p => p.page === pageNumber)
 if (!page || !Number.isInteger(page.cid) || page.cid <= 0) throw new Error('所选分集不存在')
 const player = new URL('https://api.bilibili.com/x/player/v2')
 player.searchParams.set('cid', String(page.cid))
 player.searchParams.set(
  videoId.startsWith('BV') ? 'bvid' : 'aid',
  videoId.startsWith('BV') ? videoId : videoId.slice(2)
 )
 const detail = (await json(player.href, fetcher, signal)) as {code?: number; data?: {subtitle?: {subtitles?: Track[]}}}
 if (detail.code !== 0 || !detail.data) throw new Error('无法读取此分集的字幕目录')
 const tracks = detail.data.subtitle?.subtitles || []
 if (!tracks.length) throw new Error('此分集没有站内字幕；可下载视频后在课程字幕功能中转写')
 let lastError: Error | undefined
 for (const track of preferredSubtitleTracks(tracks)) {
  try {
   // biome-ignore format: NOSONAR must stay on the await line
   const body = await json(subtitleAddress(track.subtitle_url), fetcher, signal) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
   return subtitleTrackResult(track, page, pageNumber, body)
  } catch (error) {
   signal.throwIfAborted()
   lastError = error instanceof Error ? error : new Error('字幕读取失败')
  }
 }
 throw lastError || new Error('此分集没有可用字幕')
}
