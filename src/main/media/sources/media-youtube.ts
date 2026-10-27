import Innertube, {Platform} from 'youtubei.js'
import {getQuickJS} from 'quickjs-emscripten'
import {z} from 'zod'
import {boundedMediaResponse, matchesSubtitleLanguage} from '@main/media/core/media-subtitle-download'
import type {MediaCatalog} from '@shared/media'
import type {NativeMediaSource, NativeMediaStream} from '@main/media/core/media-source'
import {mediaOptions} from '@main/media/core/media-options'
/** Remote signature code has no Node globals, file/network bindings or host callbacks. */
export async function youtubeSignature(script: string, env: Record<string, unknown> = {}) {
 if (script.length > 4 * 1024 * 1024 || Object.keys(env).length > 20) throw new Error('播放器签名脚本超过预算')
 const quick = await getQuickJS(),
  runtime = quick.newRuntime()
 runtime.setMemoryLimit(32 * 1024 * 1024)
 runtime.setMaxStackSize(1024 * 1024)
 const deadline = Date.now() + 1500
 runtime.setInterruptHandler(() => Date.now() > deadline)
 const context = runtime.newContext()
 function contextValue(context: ReturnType<typeof runtime.newContext>, value: unknown) {
  if (typeof value === 'string') return context.newString(value)
  if (typeof value === 'number') return context.newNumber(value)
  if (value === true) return context.true.dup()
  if (value === false) return context.false.dup()
  if (value === null) return context.null.dup()
  return context.undefined.dup()
 }
 try {
  for (const [name, value] of Object.entries(env)) {
   if (
    !/^[A-Za-z_$][A-Za-z0-9_$]{0,100}$/.test(name) ||
    (!['string', 'number', 'boolean', 'undefined'].includes(typeof value) && value !== null) ||
    (typeof value === 'string' && value.length > 65536)
   )
    throw new Error('签名参数无效')
   const handle = contextValue(context, value)
   try {
    context.setProp(context.global, name, handle)
   } finally {
    handle.dispose()
   }
  }
  const result = context.evalCode('(()=>{' + script + '\n})()')
  if (result.error) {
   result.error.dispose()
   throw new Error('播放器签名处理失败或达到执行预算')
  }
  try {
   const data = context.dump(result.value) as unknown
   if (JSON.stringify(data)?.length > 65536) throw new Error('签名结果超出预算')
   return z.record(z.string(), z.unknown()).parse(data)
  } finally {
   result.value.dispose()
  }
 } finally {
  context.dispose()
  runtime.dispose()
 }
}
Platform.shim.eval = (data, env) => youtubeSignature(data.output, env)
function youtubeInputParts(url: URL) {
 const host = url.hostname.replace(/^www\./, ''),
  list = url.searchParams.getAll('list'),
  video = url.searchParams.getAll('v')
 if (
  url.protocol !== 'https:' ||
  url.port ||
  url.username ||
  url.password ||
  url.hash ||
  !['youtube.com', 'm.youtube.com', 'youtu.be', 'music.youtube.com'].includes(host)
 )
  throw new Error('YouTube 网址无效')
 if (list.length > 1 || video.length > 1) throw new Error('视频或播放列表标识有歧义')
 let id = ''
 if (host === 'youtu.be') id = url.pathname.slice(1)
 else if (url.pathname === '/watch') id = video[0] || ''
 else id = /^\/(?:shorts|embed)\/([A-Za-z0-9_-]{11})\/?$/.exec(url.pathname)?.[1] || ''
 if (
  (id && !/^[A-Za-z0-9_-]{11}$/.test(id)) ||
  (list[0] && !/^[A-Za-z0-9_-]{10,100}$/.test(list[0])) ||
  (!id && !list[0])
 )
  throw new Error('请使用具体视频或播放列表网址')
 return {id, list: list[0] || ''}
}
export function youtubeInput(input: string) {
 const {id, list} = youtubeInputParts(new URL(input))
 const listSuffix = list ? '&list=' + list : ''
 return {
  id,
  list,
  url: id ? 'https://www.youtube.com/watch?v=' + id + listSuffix : 'https://www.youtube.com/playlist?list=' + list
 }
}
function checked(raw: string, media = false) {
 const url = new URL(raw)
 if (
  url.protocol !== 'https:' ||
  url.port ||
  url.username ||
  url.password ||
  url.hash ||
  !(
   media ? ['googlevideo.com'] : ['youtube.com', 'youtubei.googleapis.com', 'ytimg.com', 'googlevideo.com', 'ggpht.com']
  ).some(host => url.hostname === host || url.hostname.endsWith('.' + host))
 )
  throw new Error('YouTube 服务地址未通过核验')
 return url.href
}
type Fetcher = (url: string, init?: RequestInit) => Promise<Response>
async function youtubeStreams(formats: unknown[], yt: {session: {player?: unknown}}) {
 const streams: NativeMediaStream[] = []
 for (const raw of formats) {
  const row = raw as {
   drm_families?: unknown[]
   drm_track_type?: unknown
   is_type_otf?: boolean
   has_video: boolean
   has_audio: boolean
   itag: number | string
   mime_type: string
   height?: number
   bitrate?: number
   decipher(player: unknown): Promise<string>
  }
  if (
   row.drm_families?.length ||
   row.drm_track_type ||
   row.is_type_otf ||
   (!row.has_video && !row.has_audio) ||
   streams.some(stream => stream.id === String(row.itag))
  )
   continue
  // biome-ignore format: NOSONAR must stay on the await line
  const url = checked(await row.decipher(yt.session.player), true), // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
   codec = /codecs="([^"]+)"/.exec(row.mime_type)?.[1] || ''
  let extension = 'm4a',
   kind: NativeMediaStream['kind'] = 'audio'
  if (row.mime_type.includes('webm')) extension = 'webm'
  else if (row.has_video) extension = 'mp4'
  if (row.has_video) kind = row.has_audio ? 'combined' : 'video'
  streams.push({
   id: String(row.itag),
   kind,
   urls: [url],
   height: row.height || 0,
   codec,
   bandwidth: row.bitrate || 0,
   extension
  })
 }
 return streams
}
function youtubeCaptionTracks(
 info: {
  captions?: {
   caption_tracks?: {kind?: string; language_code: string; base_url: string; is_translatable?: boolean}[]
   translation_languages?: {language_code: string}[]
  }
 },
 settings: ReturnType<typeof mediaOptions>
) {
 const tracks: NonNullable<NativeMediaSource['subtitleTracks']> = []
 for (const track of info.captions?.caption_tracks || []) {
  const url = new URL(checked(track.base_url))
  url.searchParams.set('fmt', 'json3')
  tracks.push({
   language: (track.kind === 'asr' ? 'ai-' : '') + track.language_code,
   url: url.href,
   automatic: track.kind === 'asr',
   format: 'youtube-json3'
  })
 }
 if (!settings.translated_subtitles) return tracks
 const original = info.captions?.caption_tracks?.find(track => track.is_translatable)
 if (!original) return tracks
 for (const lang of info.captions?.translation_languages || []) {
  const name = 'ai-' + lang.language_code
  if (tracks.some(track => track.language === name) || !matchesSubtitleLanguage(name, settings.subtitle_languages))
   continue
  const url = new URL(checked(original.base_url))
  url.searchParams.set('fmt', 'json3')
  url.searchParams.set('tlang', lang.language_code)
  tracks.push({language: name, url: url.href, automatic: true, format: 'youtube-json3'})
 }
 return tracks
}
function youtubePlaylistEntry(item: unknown) {
 const row = item as {id?: unknown; content_id?: unknown; title?: unknown}
 let id: unknown
 if ('id' in row) id = row.id
 else if ('content_id' in row) id = row.content_id
 if (typeof id !== 'string' || !/^[A-Za-z0-9_-]{11}$/.test(id)) return undefined
 return {
  id,
  title: ('title' in row ? String(row.title) : id).slice(0, 240),
  url: 'https://www.youtube.com/watch?v=' + id
 }
}
export class NativeYoutubeCatalog {
 constructor(private readonly fetcher: Fetcher) {}
 private network(signal: AbortSignal): typeof fetch {
  return async (input, init) => {
   const request = new Request(input, init),
    url = checked(request.url),
    body = ['GET', 'HEAD'].includes(request.method) ? undefined : await request.text()
   if (body && body.length > 1000000) throw new Error('YouTube 请求超出预算')
   const response = await this.fetcher(url, {
    method: request.method,
    headers: request.headers,
    body,
    signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]),
    redirect: 'error',
    credentials: 'omit'
   })
   const bytes = await boundedMediaResponse(response, 16 * 1024 * 1024)
   const result = new Response(bytes, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers
   })
   Object.defineProperty(result, 'url', {value: response.url || url})
   return result
  }
 }
 private async readPlaylist(
  yt: Awaited<ReturnType<typeof Innertube.create>>,
  selected: ReturnType<typeof youtubeInput>,
  signal: AbortSignal
 ) {
  if (!selected.list) throw new Error('请提供带播放列表标识的网址，单个视频不冒充整套课程')
  let page = await yt.getPlaylist(selected.list)
  const entries: {title: string; url: string}[] = [],
   seen = new Set<string>()
  const title = page.info.title || 'YouTube 播放列表'
  for (let index = 0; index < 10; index++) {
   for (const item of page.items) {
    const row = youtubePlaylistEntry(item)
    if (!row || seen.has(row.id)) continue
    seen.add(row.id)
    entries.push({title: row.title, url: row.url})
    if (entries.length >= 100) break
   }
   if (entries.length >= 100 || !page.has_continuation) break
   signal.throwIfAborted()
   // biome-ignore format: NOSONAR must stay on the await line
   page = await page.getContinuation() // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  }
  if (!entries.length) throw new Error('未取得可选择的公开视频')
  return {url: selected.url, title, playlist: true, entries, limited: page.has_continuation || entries.length >= 100}
 }
 private async readVideo(
  yt: Awaited<ReturnType<typeof Innertube.create>>,
  selected: ReturnType<typeof youtubeInput>,
  settings: ReturnType<typeof mediaOptions>,
  signal: AbortSignal
 ) {
  if (!selected.id) throw new Error('请先选择播放列表中的具体视频')
  const info = await yt.getBasicInfo(selected.id),
   basic = info.basic_info
  if (basic.id !== selected.id || basic.is_live || basic.is_upcoming || info.playability_status?.status !== 'OK')
   throw new Error('此 YouTube 视频未确认可下载，或仍为直播')
  const duration = z.number().positive().finite().parse(basic.duration),
   formats = [...(info.streaming_data?.formats || []), ...(info.streaming_data?.adaptive_formats || [])]
  const streams = await youtubeStreams(formats, yt)
  if (!streams.length && !settings.subtitle_only)
   throw new Error('YouTube 未返回可读取格式，请检查访问权限或站点验证要求')
  const tracks = youtubeCaptionTracks({captions: info.captions}, settings)
  if (settings.format_ids && !settings.format_ids.split('+').every(id => streams.some(stream => stream.id === id)))
   throw new Error('YouTube 格式已改变，请重新读取')
  signal.throwIfAborted()
  return {
   platform: 'youtube',
   sourceId: selected.id,
   identity: 'youtube:' + selected.id,
   streams,
   subtitleTracks: tracks,
   description: basic.short_description || '',
   downloadHeaders: {Referer: 'https://www.youtube.com/', 'User-Agent': 'Mozilla/5.0'},
   thumbnail: basic.thumbnail?.at(-1)?.url ? checked(basic.thumbnail.at(-1)!.url) : undefined,
   catalog: {
    url: 'https://www.youtube.com/watch?v=' + selected.id,
    title: (basic.title || selected.id).slice(0, 240),
    playlist: false,
    duration,
    heights: [...new Set(streams.filter(row => row.kind !== 'audio').map(row => row.height))].sort((a, b) => a - b),
    subtitles: tracks.map(track => track.language),
    formats: streams.map(row => ({
     id: row.id,
     height: row.height || null,
     extension: row.extension,
     video_codec: row.kind === 'audio' ? 'none' : row.codec,
     audio_codec: row.kind === 'video' ? 'none' : row.codec
    }))
   }
  }
 }
 async read(
  input: string,
  playlist: boolean,
  signal: AbortSignal,
  options: unknown = {}
 ): Promise<MediaCatalog | NativeMediaSource> {
  const selected = youtubeInput(input),
   settings = mediaOptions(options)
  const yt = await Innertube.create({
   fetch: this.network(signal),
   generate_session_locally: true,
   retrieve_innertube_config: false,
   enable_session_cache: false,
   lang: 'zh-CN'
  })
  return playlist ? this.readPlaylist(yt, selected, signal) : this.readVideo(yt, selected, settings, signal)
 }
}
