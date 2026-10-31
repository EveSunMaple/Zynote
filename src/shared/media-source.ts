export type MediaSource =
 | {kind: 'local'; status: 'unresolved' | 'resolved'; path: string; sha256: string}
 | {kind: 'bili'; status: 'unresolved'; url: string; videoId: string; page: number}
 | {
    kind: 'bili'
    status: 'resolved'
    url: string
    videoId: string
    page: number
    bvid: string
    cid: number
    sha256?: string
   }

// Catalog resolution proves identity; only a verified digest proves content readiness.
export function mediaSourceSummary(source?: MediaSource): string {
 if (!source) return ''
 if (source.kind === 'local') return source.status === 'resolved' ? '本地视频副本已核验' : '本地视频待核验'
 const state = source.status === 'unresolved' ? '待解析目录' : source.sha256 ? '视频副本已核验' : '目录身份已解析'
 return `哔哩哔哩 P${source.page} · ${state}`
}
