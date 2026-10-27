import {dispatch, type IpcHandler} from '@main/ipc/handler'
import type {GoCore} from '@main/backend/go-core'

const goShadowActions = new Set([
 'app:info',
 'settings:get',
 'appearance:get',
 'identity:get',
 'course:detail',
 'library:list',
 'sessions',
 'lecture:detail'
])

export const goPrimaryActions = new Set([
 'appearance:save',
 'appearance:clear',
 'identity:save',
 'course:create',
 'session:create',
 'db:bootstrap',
 'course:delete',
 'session:delete',
 'lecture:delete',
 'lecture:create',
 'lecture:transcript',
 'lecture:courseware',
 'files:add',
 'files:remove',
 'questions:save',
 'courses',
 'study:get',
 'study:save',
 'study:parse',
 'subtitles:generate',
 'notes:generate',
 'practice:generate',
 'practice:answer',
 'practice:prepare',
 'subtitle-sync:list',
 'subtitle-sync:start',
 'subtitle-sync:mixed',
 'subtitle-sync:resume',
 'subtitle-sync:attach',
 'subtitle-derived:list',
 'subtitle-derived:resume',
 'subtitle-derived:start',
 'settings:save',
 'appearance:image',
 'identity:avatar',
 'library:open',
 'library:open-file',
 'library:reveal',
 'library:delete',
 'media:open',
 'textbook:pick',
 'textbook:bytes',
 'textbook:open',
 'textbook:manual',
 'textbook:clear',
 'textbook:apply',
 'textbook:analyze',
 'textbook:analyze-images',
 'textbook:ppt-source',
 'textbook:ppt-ocr',
 'visual:list',
 'visual:prepare',
 'visual:prepare-resume',
 'visual:recognize',
 'visual:recover-output',
 'ppt-extract:analyze',
 'ppt-extract:export',
 'zhiyun:search',
 'zhiyun:lessons',
 'zhiyun:export',
 'zhiyun:login',
 'zhiyun:refresh',
 'zhiyun:back',
 'zhiyun:bounds',
 'practice:submit',
 'practice:reset',
 'subtitles:save',
 'subtitles:import',
 'questions:split',
 'notes:save',
 'chat',
 'cancel',
 'export',
 'job:current',
 'settings:models',
 'settings:probe',
 'settings:test',
 'db:health',
 'db:backup',
 'knowledge:list',
 'knowledge:generation',
 'knowledge:read',
 'knowledge:import',
 'knowledge:citation',
 'knowledge:delete',
 'knowledge:organize',
 'knowledge:rebuild',
 'knowledge:search',
 'knowledge:vector-status',
 'knowledge:vector-enable',
 'knowledge:vector-rebuild',
 'knowledge:ask',
 'knowledge:classification-suggest',
 'knowledge:classification-options',
 'knowledge:classification-read',
 'knowledge:classification-save',
 'im:draft',
 'tools:skill-open',
 'runtime:install',
 'runtime:status',
 'media:options',
 'media:list',
 'media:retry',
 'media:cancel',
 'media:file',
 'media:auth-import',
 'media:auth-list',
 'media:auth-remove',
 'media:catalog',
 'media:create',
 'media:discussion',
 'media:discussion-import',
 'bili-resources:status',
 'bili-resources:browse',
 'bili-resources:qr',
 'bili-resources:poll',
 'bili-resources:logout',
 'bili-resources:folders',
 'bili-resources:videos',
 'video-ppt:pending',
 'video-ppt:restore',
 'video-ppt:subtitle-import',
 'video-ppt:subtitle-save',
 'video-ppt:subtitle-export',
 'video-ppt:series',
 'video-ppt:subtitle',
 'video-ppt:subtitle-transcribe-local',
 'video-ppt:resume-subtitle',
 'video-ppt:source',
 'video-ppt:extract',
 'video-ppt:export',
 'video-ppt:auto',
 'video-ppt:summary',
 'video-ppt:notes-export',
 'video-ppt:batch',
 'video-ppt:batch-subtitles',
 'video-ppt:subtitle-transcribe',
 'tools:request'
])

const deleteSubjects = new Map([
 ['course:delete', '课程'],
 ['session:delete', '会话'],
 ['lecture:delete', '课次']
])

const compareCodeUnit = (a: string, b: string) => Number(a > b) - Number(a < b)

function stable(value: unknown): unknown {
 if (Array.isArray(value)) return value.map(item => stable(item))
 if (value && typeof value === 'object') {
  const result: Record<string, unknown> = {}
  for (const key of Object.keys(value as Record<string, unknown>).sort(compareCodeUnit))
   result[key] = stable((value as Record<string, unknown>)[key])
  return result
 }
 return value
}

export function createRoutedDispatch(
 handlers: readonly IpcHandler[],
 goCore: GoCore | undefined,
 root: string,
 activeJobs: () => number,
 diagnostic: (
  root: string,
  component: string,
  event: string,
  fields?: {action?: string; code?: string; keys?: string}
 ) => void
) {
 return async (action: string, object: Record<string, unknown>): Promise<unknown> => {
  const subject = deleteSubjects.get(action)
  if (subject && activeJobs()) throw new Error('请等待当前任务完成后再删除' + subject)
  if (goCore?.currentStatus !== 'ready') return dispatch(handlers, action, object)
  if (
   process.platform === 'win32' &&
   action === 'tools:request' &&
   (object as {args?: {action?: unknown}}).args?.action === 'im'
  )
   return dispatch(handlers, action, object)
  if (goPrimaryActions.has(action)) return goCore.call(action, object)
  if (!goShadowActions.has(action)) return dispatch(handlers, action, object)
  const [goSettled, tsSettled] = await Promise.all([
   goCore.call(action, object).then(
    value => ({ok: true as const, value}),
    error => ({ok: false as const, error: error instanceof Error ? error.message : String(error)})
   ),
   dispatch(handlers, action, object).then(
    value => ({ok: true as const, value}),
    error => ({ok: false as const, error})
   )
  ])
  if (!goSettled.ok) {
   diagnostic(root, 'shadow', 'go-failed', {action})
   if (!tsSettled.ok) throw tsSettled.error
   return tsSettled.value
  }
  if (tsSettled.ok) {
   const left = stable(goSettled.value),
    right = stable(tsSettled.value)
   if (JSON.stringify(left) !== JSON.stringify(right)) {
    const leftKeys = new Set(Object.keys((left ?? {}) as Record<string, unknown>)),
     rightKeys = new Set(Object.keys((right ?? {}) as Record<string, unknown>))
    const differing = [
     ...[...leftKeys].filter(key => !rightKeys.has(key)).map(key => 'only-go:' + key),
     ...[...rightKeys].filter(key => !leftKeys.has(key)).map(key => 'only-ts:' + key)
    ]
    if (!differing.length)
     for (const key of leftKeys)
      if (
       JSON.stringify(stable((left as Record<string, unknown>)[key])) !==
       JSON.stringify(stable((right as Record<string, unknown>)[key]))
      )
       differing.push('diff:' + key)
    diagnostic(root, 'shadow', 'diff', {action, code: 'RESPONSE_DIFF', keys: differing.join(',').slice(0, 300)})
   }
  } else diagnostic(root, 'shadow', 'ts-failed', {action})
  return goSettled.value
 }
}
