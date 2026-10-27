import type {NativeMediaStream} from '@main/media/bilibili/media-bili'
/** A documented native subset, rather than passing unparsed downloader expressions to a shell. */
export function mediaSort(input: string) {
 return input
  ? input.split(',').map(token => {
     const match = /^(\+)?(res|height|br|tbr|vcodec|acodec)(?::([A-Za-z0-9]+))?$/.exec(token)
     if (!match) throw new Error('原生格式排序支持 res、height、br、tbr、vcodec、acodec，可用 + 表示升序')
     if (match[3] && !['vcodec', 'acodec'].includes(match[2]))
      throw new Error('数值排序不使用额外参数，请通过画质或音质上限设置')
     return {ascending: !!match[1], field: match[2], preferred: match[3] || ''}
    })
  : []
}
export function sortMediaStreams(rows: NativeMediaStream[], input: string, freeFormats = false) {
 const fields = mediaSort(input)
 const codecScore = (codec: string) => {
  if (/^(?:av01|av1)/.test(codec)) return 4
  if (codec.startsWith('vp')) return 3
  if (/^(?:hev|hvc)/.test(codec)) return 2
  if (/^(?:avc|h264)/.test(codec)) return 1
  return 0
 }
 return [...rows].sort((a, b) => {
  for (const field of fields) {
   let av: number, bv: number
   if (['res', 'height'].includes(field.field)) {
    av = a.height
    bv = b.height
   } else if (['br', 'tbr'].includes(field.field)) {
    av = a.bandwidth
    bv = b.bandwidth
   } else {
    let preferred = field.preferred
    if (field.preferred === 'h264') preferred = 'avc'
    else if (field.preferred === 'h265') preferred = 'hev'
    else if (field.preferred === 'av1') preferred = 'av01'
    av = preferred ? Number(a.codec.startsWith(preferred)) : codecScore(a.codec)
    bv = preferred ? Number(b.codec.startsWith(preferred)) : codecScore(b.codec)
   }
   if (av !== bv) return field.ascending ? av - bv : bv - av
  }
  if (freeFormats) {
   const free = (row: NativeMediaStream) =>
    Number(row.extension === 'webm' || /^vp|^av01|^opus|^vorbis/.test(row.codec))
   if (free(a) !== free(b)) return free(b) - free(a)
  }
  return b.height - a.height || b.bandwidth - a.bandwidth || a.id.localeCompare(b.id)
 })
}
