import {afterEach, describe, expect, it, vi} from 'vitest'
import type {StudyEntry} from '@shared/study'
import {needsTermReview} from './useScheduleMatching'

vi.mock('@renderer/ui/ToastHost', () => ({errorMessage: String}))
vi.mock('@renderer/features/study/datetime', () => import('./datetime'))

describe('monthly personal timetable matching', () => {
 afterEach(() => vi.useRealTimers())
 const slot = (endsAt: string): StudyEntry => ({id: 'test', title: '数据思维', courseId: '', chapter: '', endsAt})

 it('keeps older and future dated classes as candidates at the Shanghai month boundary', () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2031-08-31T16:05:00Z'))
  expect(needsTermReview(slot('2031-08-31T15:59:00Z'), 'personal-calendar')).toBe(true)
  expect(needsTermReview(slot('2031-08-31T16:03:00Z'), 'personal-calendar')).toBe(false)
  expect(needsTermReview(slot('2031-10-01T09:00:00+08:00'), 'personal-calendar')).toBe(true)
 })

 it('keeps recurring classes and catalog results eligible for normal matching', () => {
  const previous = slot('2000-10-01T09:00:00+08:00')
  expect(needsTermReview({...previous, weekly: true}, 'personal-calendar')).toBe(false)
  expect(needsTermReview(previous, 'catalog')).toBe(false)
 })
})
