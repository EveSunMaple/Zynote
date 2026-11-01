import {describe, expect, it, vi} from 'vitest'
import {bookmarkPage, firstBodyBookmark, textbookBookmarks} from './textbook-bookmarks'

describe('textbook bookmark navigation', () => {
 const document = {
  numPages: 423,
  getDestination: vi.fn(async (name: string) => (name === 'body' ? [{num: 77, gen: 0}] : null)),
  getPageIndex: vi.fn(async () => 16)
 }
 it('locates the first chapter without resolving the full outline', async () => {
  const rows = textbookBookmarks([
   {title: '目 录', dest: [10], items: []},
   {title: '正文', dest: null, items: [{title: '第 １ 章 示例', dest: 'body', items: []}]}
  ])
  expect(rows[1].parent).toBe('正文')
  expect(document.getPageIndex).not.toHaveBeenCalled()
  const body = firstBodyBookmark(rows)
  expect(body).toBe(rows[1])
  expect(await bookmarkPage(document, rows[0].dest)).toBe(11)
  expect(await bookmarkPage(document, rows[1].dest)).toBe(17)
  expect(document.getPageIndex).toHaveBeenCalledTimes(1)
 })
 it('allows explicit introductions and avoids guessing from a later chapter or contents', () => {
  const rows = ['目录', 'Chapter 10', '第十一章'].map(label => ({label, parent: '', dest: [0]}))
  expect(firstBodyBookmark(rows)).toBeUndefined()
  expect(firstBodyBookmark([...rows, {label: '绪论', parent: '', dest: [2]}])?.label).toBe('绪论')
  expect(firstBodyBookmark([...rows, {label: 'Chapter I. Motion', parent: '', dest: [3]}])?.dest).toEqual([3])
  expect(textbookBookmarks(null)).toEqual([])
 })
 it('rejects missing, invalid and out-of-book destinations', async () => {
  for (const dest of ['missing', [], ['URI'], [-1], [423], [0.5]]) {
   expect(await bookmarkPage(document, dest)).toBeNull()
  }
 })
})
