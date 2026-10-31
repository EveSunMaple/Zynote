import {describe, expect, it} from 'vitest'
import {verifiedNoteAnchor, type VideoNotePoint} from './video-notes'

describe('subtitle summary anchors', () => {
 it('requires the current subtitle content identity and exact cue identity', () => {
  const hash = 'a'.repeat(64)
  const point: VideoNotePoint = {
   cue: 0,
   quote: 'Original',
   point: 'Point',
   topic: '',
   timestamp: '00:00:01',
   anchor: {sourceHash: hash, cueIndex: 0, startMs: 1125, endMs: 3750, text: 'Original words'}
  }
  expect(verifiedNoteAnchor(point, hash, hash)).toEqual(point.anchor)
  expect(verifiedNoteAnchor(point, hash, 'b'.repeat(64))).toBeNull()
  expect(verifiedNoteAnchor(point, hash, undefined)).toBeNull()
  expect(verifiedNoteAnchor({...point, anchor: undefined}, hash, hash)).toBeNull()
  for (const anchor of [
   {...point.anchor!, sourceHash: 'b'.repeat(64)},
   {...point.anchor!, cueIndex: 1},
   {...point.anchor!, startMs: Number.NaN},
   {...point.anchor!, endMs: 1125}
  ])
   expect(verifiedNoteAnchor({...point, anchor}, hash, hash)).toBeNull()
 })
})
