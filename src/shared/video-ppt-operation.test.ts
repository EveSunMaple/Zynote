import {describe, expect, it} from 'vitest'
import {retainVideoOperation, settleLocalSubtitleOperation} from './video-ppt-operation'

describe('PPT submission identity', () => {
 it('binds batch selection and order while retaining an identical retry', () => {
  let ids = 0
  const createId = () => `batch-${++ids}`
  const input = {url: 'https://example/video', pages: [1, 2], prompt: 'crop'}
  const draft = retainVideoOperation(null, 'video-ppt:batch', input, createId)
  expect(retainVideoOperation(draft, 'video-ppt:batch', {...input, pages: [1, 2]}, createId)).toBe(draft)
  for (const pages of [[2, 1], [1], [1, 2, 3]]) {
   expect(retainVideoOperation(draft, 'video-ppt:batch', {...input, pages}, createId).id).not.toBe(draft.id)
  }
 })
 it('retains retry identity and changes it when meaningful inputs change', () => {
  let ids = 0
  const createId = () => `operation-${++ids}`
  const input = {url: 'https://example/video', prompt: 'crop', courseId: 'course'}
  const draft = retainVideoOperation(null, 'video-ppt:auto', input, createId)
  expect(retainVideoOperation(draft, 'video-ppt:auto', {...input}, createId)).toBe(draft)
  for (const changed of [
   {...input, prompt: 'changed'},
   {...input, courseId: 'other'},
   {...input, url: 'other'}
  ]) {
   expect(retainVideoOperation(draft, 'video-ppt:auto', changed, createId).id).not.toBe(draft.id)
  }
  expect(retainVideoOperation(draft, 'video-ppt:source', input, createId).id).not.toBe(draft.id)
 })

 it('keeps failed local selection for resume and releases successful or cancelled selections', () => {
  let ids = 0
  const createId = () => `local-${++ids}`
  const action = 'video-ppt:subtitle-transcribe-local'
  const draft = retainVideoOperation(null, action, {courseId: 'course'}, createId)
  const failed = settleLocalSubtitleOperation(draft, 'failed')
  expect(retainVideoOperation(failed, action, {courseId: 'course'}, createId)).toBe(draft)
  for (const outcome of ['completed', 'cancelled'] as const) {
   const released = settleLocalSubtitleOperation(draft, outcome)
   expect(retainVideoOperation(released, action, {courseId: 'course'}, createId).id).not.toBe(draft.id)
  }
  expect(retainVideoOperation(null, action, {courseId: 'course'}, createId).id).not.toBe(draft.id)
  expect(retainVideoOperation(failed, action, {courseId: 'changed'}, createId).id).not.toBe(draft.id)
 })
})
