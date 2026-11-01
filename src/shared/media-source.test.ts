import {describe, expect, it} from 'vitest'
import {mediaSourceSummary} from './media-source.js'

describe('media source readiness', () => {
 it('distinguishes catalog identity from verified content', () => {
  const identity = {
   kind: 'bili',
   status: 'resolved',
   url: 'https://www.bilibili.com/video/BV1234567890/?p=2',
   videoId: 'BV1234567890',
   page: 2,
   bvid: 'BV1234567890',
   cid: 7
  } as const
  expect(mediaSourceSummary(identity)).toBe('哔哩哔哩 P2 · 目录身份已解析')
  expect(mediaSourceSummary({...identity, sha256: 'a'.repeat(64)})).toBe('哔哩哔哩 P2 · 视频副本已核验')
  expect(mediaSourceSummary()).toBe('')
 })
})
