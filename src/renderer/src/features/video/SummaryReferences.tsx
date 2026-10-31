import {Button} from '@heroui/react'
import {useEffect, useRef, useState} from 'react'
import {verifiedNoteAnchor, type SubtitleCueAnchor, type VideoNoteResult} from '@shared/video-notes'
import {time} from '@shared/srt'

export function SummaryReferences({summary, srt}: Readonly<{summary: VideoNoteResult; srt: string}>) {
 const [source, setSource] = useState<{srt: string; hash: string} | null>(null)
 const [selected, setSelected] = useState<SubtitleCueAnchor | null>(null)
 const target = useRef<HTMLQuoteElement>(null)
 useEffect(() => {
  let active = true
  void crypto.subtle
   .digest('SHA-256', new TextEncoder().encode(srt))
   .then(bytes => {
    if (active)
     setSource({srt, hash: Array.from(new Uint8Array(bytes), byte => byte.toString(16).padStart(2, '0')).join('')})
   })
   .catch(() => {
    if (active) setSource(null)
   })
  return () => {
   active = false
  }
 }, [srt])
 const currentHash = source?.srt === srt ? source.hash : undefined
 const visible = selected?.sourceHash === currentHash && summary.sourceHash === currentHash ? selected : null
 useEffect(() => {
  if (visible) target.current?.scrollIntoView({block: 'nearest'})
 }, [visible])
 return (
  <section className="learning-summary" aria-label="摘要引用原文">
   <h3>核对字幕原文</h3>
   <p className="hint">选择时间点查看对应字幕。这里定位字幕原文，不控制视频播放。</p>
   <div className="row">
    {summary.points.map((point, index) => {
     const anchor = verifiedNoteAnchor(point, summary.sourceHash, currentHash)
     return (
      <Button
       key={`${index}-${point.cue}`}
       variant="tertiary"
       size="sm"
       isDisabled={!anchor}
       onClick={() => {
        if (anchor) setSelected(anchor)
       }}
       aria-label={`查看字幕 ${point.timestamp}：${point.quote}`}
      >
       {point.timestamp} · 字幕 {point.cue + 1}
      </Button>
     )
    })}
   </div>
   {!currentHash || currentHash !== summary.sourceHash ? (
    <p className="hint">当前字幕与摘要来源尚未核对或已经不同，引用定位暂不可用。</p>
   ) : null}
   {visible && (
    <blockquote ref={target}>
     <p>
      字幕 {visible.cueIndex + 1} · {time(visible.startMs)}–{time(visible.endMs)}
     </p>
     <p>{visible.text}</p>
    </blockquote>
   )}
  </section>
 )
}
