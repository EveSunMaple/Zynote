import {dispatch, type IpcHandler} from '@main/ipc/handler'
import type {GoCore} from '@main/backend/go-core'

export const goPrimaryActions = new Set([
 'app:info',
 'settings:get',
 'appearance:get',
 'identity:get',
 'course:detail',
 'library:list'
])

function stable(value: unknown): unknown {
 if (Array.isArray(value)) return value.map(item => stable(item))
 if (value && typeof value === 'object') {
  const result: Record<string, unknown> = {}
  for (const key of Object.keys(value as Record<string, unknown>).sort())
   result[key] = stable((value as Record<string, unknown>)[key])
  return result
 }
 return value
}

export function createRoutedDispatch(
 handlers: readonly IpcHandler[],
 goCore: GoCore | undefined,
 root: string,
 diagnostic: (
  root: string,
  component: string,
  event: string,
  fields?: {action?: string; code?: string; keys?: string}
 ) => void
) {
 return async (action: string, object: Record<string, unknown>): Promise<unknown> => {
  if (!goCore || goCore.currentStatus !== 'ready' || !goPrimaryActions.has(action))
   return dispatch(handlers, action, object)
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
