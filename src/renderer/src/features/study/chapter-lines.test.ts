import {describe, expect, it} from 'vitest'
import {candidatesForChapter, groupFragments, headingLine, isContentsPage, type ChapterLine} from './chapter-lines'
const line = (page: number, n: number, text: string, marked = false): ChapterLine => ({
 page,
 line: n,
 text,
 rect: [10, 50, 100, 12],
 marked
})
describe('chapter references', () => {
 it('uses actual visual lines, keeps a second column separate and merges split glyphs', () => {
  expect(
   groupFragments([
    {text: '限', box: [28, 10.2, 9, 10]},
    {text: '另一栏', box: [130, 11, 40, 10]},
    {text: '极', box: [18, 10, 9, 10]},
    {text: '后续', box: [20, 36, 20, 9]}
   ]).map(row => row.text)
  ).toEqual(['极限', '另一栏', '后续'])
 })
 it('ignores contents rows and collects definitions past the first five pages', () => {
  expect(isContentsPage([line(1, 1, '目次'), line(1, 2, '第一章 极限…… 6'), line(1, 3, '第二章 求导…… 15')])).toBe(true)
  const rows = Array.from({length: 12}, (_, i) => [
   line(i + 2, 1, '第一章 极限'),
   line(i + 2, 2, '定义：数列的极限是……', i === 11)
  ])
  const hits = candidatesForChapter(rows)
  expect(hits).toHaveLength(12)
  expect(hits.at(-1)).toMatchObject({page: 13, line: 2, marked: true})
  expect(headingLine(rows[0], '第一章 极限')?.line).toBe(1)
  expect(headingLine(rows[0], '第三章 积分')).toBeUndefined()
 })
})
