import {describe, expect, it} from 'vitest'
import {retainVideoOperation} from './video-ppt-operation'

describe('PPT submission identity', () => {
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
})
