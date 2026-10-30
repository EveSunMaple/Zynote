import {describe, expect, it, vi} from 'vitest'
vi.mock('electron', () => ({BrowserWindow: class {}, Menu: {}, session: {}}))
import {biliPage, biliImport} from './browser'
describe('Bilibili navigation and import boundary', () => {
 it('accepts official pages and preserves a selected part', () => {
  expect(biliPage('https://space.bilibili.com/123').hostname).toBe('space.bilibili.com')
  expect(biliImport('https://www.bilibili.com/video/BV1xx411c7mD/?p=5&tracking=abc#x')).toBe(
   'https://www.bilibili.com/video/BV1xx411c7mD/?p=5'
  )
 })
 it.each([
  'https://bilibili.com.evil.test/',
  'http://www.bilibili.com/',
  'https://user:pass@www.bilibili.com/',
  'https://www.bilibili.com:444/',
  'javascript:alert(1)'
 ])('rejects untrusted navigation %s', value => expect(() => biliPage(value)).toThrow())
 it.each([
  'https://www.bilibili.com/',
  'https://space.bilibili.com/video/BV1xx411c7mD/',
  'https://www.bilibili.com/video/BV1xx411c7mD/?p=0'
 ])('rejects non-video/invalid part %s', value => expect(() => biliImport(value)).toThrow())
})
