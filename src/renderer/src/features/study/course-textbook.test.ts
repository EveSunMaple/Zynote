import {describe, expect, it} from 'vitest'
import type {StudyEntry} from '@shared/study'
import {setSessionTextbook} from './course-textbook'

const slot = (id: string, fields: Partial<StudyEntry> = {}): StudyEntry => ({
 id,
 courseId: '',
 chapter: '',
 endsAt: '',
 title: '数学分析',
 weekly: true,
 ...fields
})

describe('course textbook assignment', () => {
 it('shares one book across explicitly checked sessions and can unlink just one', () => {
  const rows = [
   slot('a', {zhiyunCourseId: '1', textbookId: 'old'}),
   slot('b', {zhiyunCourseId: '1'}),
   slot('c', {zhiyunCourseId: '1', textbookId: 'lab'}),
   slot('d', {zhiyunCourseId: '2'}),
   slot('e')
  ]
  const associated = ['a', 'b', 'e'].reduce((list, id) => setSessionTextbook(list, id, 'new', true), rows)
  expect(associated.map(row => row.textbookId)).toEqual(['new', 'new', 'lab', undefined, 'new'])
  expect(setSessionTextbook(associated, 'b', 'new', false, 'new').map(row => row.textbookId)).toEqual([
   'new',
   undefined,
   'lab',
   undefined,
   'new'
  ])
 })
 it('preserves another book when a pending replacement is unchecked', () => {
  const rows = [slot('a', {textbookId: 'old'}), slot('b', {textbookId: 'lab'})]
  const replaced = setSessionTextbook(rows, 'a', 'new', true)
  expect(setSessionTextbook(replaced, 'a', 'new', false, 'old')).toEqual(rows)
  expect(setSessionTextbook(rows, 'b', 'new', false)).toEqual(rows)
 })
})
