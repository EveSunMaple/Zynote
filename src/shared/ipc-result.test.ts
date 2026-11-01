import {describe, expect, it} from 'vitest'
import {
 assistantFailure,
 backendFailure,
 bridgeFailure,
 localAssistantFailure,
 unwrapAssistantResult
} from './ipc-result'

describe('assistant IPC boundary', () => {
 it('preserves stable errors and legacy message/String catches without wire data or functions', () => {
  for (const code of [
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
   'INTERNAL'
  ]) {
   const wire = {
    code,
    message: ' controlled\n message ',
    data: {secret: 'not exported'},
    toString: () => {
     throw new Error('wire function invoked')
    }
   }
   const error = bridgeFailure(backendFailure(wire))
   expect(error.code).toBe(code)
   expect(error.message).toBe('controlled message')
   expect(String(error)).toBe('controlled message')
   expect(Object.keys(error).sort()).toEqual(['code', 'message', 'toString'])
  }
 })
 it('uses generic unknown wire failures but retains ordinary actionable shell messages', () => {
  expect(assistantFailure({code: 'UNTRUSTED', message: 'hidden secret', data: 'hidden'})).toEqual({
   code: 'REQUEST_FAILED',
   message: '操作失败，请稍后重试'
  })
  expect(localAssistantFailure(backendFailure({code: 'UNTRUSTED', message: 'hidden secret'})).message).not.toContain(
   'hidden'
  )
  expect(localAssistantFailure(new Error(' 文件过大\n请缩小文件 '))).toEqual({
   code: 'REQUEST_FAILED',
   message: '文件过大 请缩小文件'
  })
  expect(assistantFailure({code: 42, message: 'hidden'}).message).not.toContain('hidden')
 })
 it('unwraps successful values without shape changes and rejects invalid envelopes', () => {
  for (const value of [null, 7, 'raw', false, {nested: ['original']}])
   expect(unwrapAssistantResult({ok: true, result: value})).toBe(value)
  expect(() => unwrapAssistantResult({ok: false, error: {code: 'BUSY', message: '任务进行中'}})).toThrow()
 })
})
