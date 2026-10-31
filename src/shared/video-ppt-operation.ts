export type VideoOperationDraft = {key: string; id: string}
export type VideoStartInput = {path?: string; url?: string; prompt?: string; courseId?: string}

export function retainVideoOperation(
 previous: VideoOperationDraft | null,
 action: 'video-ppt:source' | 'video-ppt:auto',
 input: VideoStartInput,
 createId: () => string
): VideoOperationDraft {
 const key = JSON.stringify([
  action,
  input.path || '',
  input.url?.trim() || '',
  input.prompt || '',
  input.courseId || ''
 ])
 return previous?.key === key ? previous : {key, id: createId()}
}
