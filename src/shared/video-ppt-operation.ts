export type VideoOperationDraft = {key: string; id: string}
export type VideoStartInput = {path?: string; url?: string; prompt?: string; courseId?: string; pages?: number[]}

export function retainVideoOperation(
 previous: VideoOperationDraft | null,
 action:
  | 'video-ppt:source'
  | 'video-ppt:auto'
  | 'video-ppt:batch'
  | 'video-ppt:batch-subtitles'
  | 'video-ppt:subtitle-transcribe'
  | 'video-ppt:subtitle-transcribe-local',
 input: VideoStartInput,
 createId: () => string
): VideoOperationDraft {
 const key = JSON.stringify([
  action,
  input.path || '',
  input.url?.trim() || '',
  input.prompt || '',
  input.courseId || '',
  input.pages || []
 ])
 return previous?.key === key ? previous : {key, id: createId()}
}

export function settleLocalSubtitleOperation(
 draft: VideoOperationDraft,
 outcome: 'completed' | 'cancelled' | 'failed'
): VideoOperationDraft | null {
 return outcome === 'failed' ? draft : null
}
