const codes = new Set([
 'INPUT_INVALID',
 'NO_SUBTITLE',
 'AUTH_EXPIRED',
 'RATE_LIMITED',
 'TIMEOUT',
 'NOT_FOUND',
 'CONFLICT',
 'BUSY',
 'CANCELLED',
 'UNAVAILABLE',
 'INTERNAL',
 'REQUEST_FAILED'
])
const fallbackMessage = '操作失败，请稍后重试'
export type AssistantFailure = {code: string; message: string}
export type AssistantResult<T = unknown> = {ok: true; result: T} | {ok: false; error: AssistantFailure}

/** Only code/message cross the bridge. Unknown wire details are never copied. */
export function assistantFailure(reason: unknown): AssistantFailure {
 if (reason && typeof reason === 'object') {
  const value = reason as {code?: unknown; message?: unknown}
  if (typeof value.code === 'string' && codes.has(value.code)) {
   const message = typeof value.message === 'string' ? value.message.replace(/\s+/g, ' ').trim().slice(0, 300) : ''
   return {code: value.code, message: message || fallbackMessage}
  }
 }
 return {code: 'REQUEST_FAILED', message: fallbackMessage}
}

/** Local shell Errors already carry user-facing messages. Preserve those, but
 * never reopen a wire failure that already has an unknown/normalized code. */
export function localAssistantFailure(reason: unknown): AssistantFailure {
 if (reason instanceof Error && !('code' in reason)) {
  const message = reason.message.replace(/\s+/g, ' ').trim().slice(0, 300)
  return {code: 'REQUEST_FAILED', message: message || fallbackMessage}
 }
 return assistantFailure(reason)
}

/** Created in preload, never copied from a wire object. String compatibility is
 * needed by existing catches; the closure returns only its sanitized message. */
export function bridgeFailure(reason: unknown) {
 const {code, message} = assistantFailure(reason)
 return {code, message, toString: () => message}
}

export function unwrapAssistantResult<T>(response: AssistantResult<T>): T {
 if (response?.ok === true && 'result' in response) return response.result
 throw bridgeFailure(response?.ok === false ? response.error : undefined)
}

export function backendFailure(reason: unknown): Error & AssistantFailure {
 const failure = assistantFailure(reason)
 return Object.assign(new Error(failure.message), {code: failure.code})
}
