import {beforeEach, describe, expect, it, vi} from 'vitest'
const fake = vi.hoisted(() => ({
 windows: [] as any[],
 menus: [] as any[],
 order: [] as string[],
 cookies: {set: vi.fn(async (_details: Record<string, unknown>) => {}), flushStore: vi.fn(async () => {})},
 clearStorageData: vi.fn(async () => {})
}))
vi.mock('electron', () => ({
 session: {
  fromPartition: (partition: string) => {
   expect(partition).toBe('persist:zynote-bilibili')
   return {cookies: fake.cookies, clearStorageData: fake.clearStorageData}
  }
 },
 Menu: {
  buildFromTemplate: (items: unknown) => {
   fake.menus.push(items)
   return {popup: vi.fn()}
  }
 },
 BrowserWindow: class {
  events: Record<string, (...args: any[]) => void> = {}
  handlers: Record<string, (...args: any[]) => void> = {}
  url = ''
  destroyed = false
  webContents = {
   on: (name: string, fn: (...args: any[]) => void) => {
    this.handlers[name] = fn
   },
   setWindowOpenHandler: vi.fn(),
   getURL: () => this.url,
   navigationHistory: {canGoBack: () => false, canGoForward: () => false},
   reload: vi.fn()
  }
  constructor(public options: any) {
   fake.windows.push(this)
  }
  on(name: string, fn: (...args: any[]) => void) {
   this.events[name] = fn
  }
  setMenu() {}
  setTitle() {}
  async loadURL(url: string) {
   this.url = url
   fake.order.push('open')
  }
  destroy() {
   this.destroyed = true
   this.events.closed?.()
   fake.order.push('destroy')
  }
 }
}))
import {BiliBrowser} from './browser'
import type {BrowserWindow} from 'electron'
describe('trusted browser lifecycle', () => {
 beforeEach(() => {
  fake.windows.length = 0
  fake.menus.length = 0
  fake.order.length = 0
  vi.clearAllMocks()
 })
 it('restores scoped cookies, isolates remote pages, imports the current part and clears on logout', async () => {
  const send = vi.fn(),
   owner = {show: vi.fn(), focus: vi.fn()}
  const browser = new BiliBrowser(owner as unknown as BrowserWindow, send)
  await browser.open('https://www.bilibili.com/video/BV1xx411c7mD/?p=3', [
   {
    domain: 'bilibili.com',
    name: 'SESSDATA',
    value: 'fixture',
    path: '/',
    hostOnly: true,
    secure: true,
    httpOnly: true,
    expires: 0
   }
  ])
  const window = fake.windows[0]
  expect(window.options.webPreferences).toMatchObject({
   nodeIntegration: false,
   contextIsolation: true,
   sandbox: true,
   webSecurity: true
  })
  expect(window.options.webPreferences.preload).toBeUndefined()
  expect(fake.cookies.set.mock.calls[0][0]).not.toHaveProperty('domain')
  fake.menus[0][0].submenu.find((item: any) => item.label === '导入当前视频到视频摘要').click()
  expect(send).toHaveBeenCalledWith({
   kind: 'bili-import',
   url: 'https://www.bilibili.com/video/BV1xx411c7mD/?p=3',
   mode: 'videoNotes'
  })
  const preventDefault = vi.fn()
  window.handlers['will-navigate']({preventDefault}, 'https://evil.test/')
  expect(preventDefault).toHaveBeenCalled()
  await browser.clear()
  expect(window.destroyed).toBe(true)
  expect(fake.clearStorageData).toHaveBeenCalledTimes(2)
 })
 it('serializes pending open before logout so no stale window survives', async () => {
  const browser = new BiliBrowser({} as BrowserWindow, vi.fn())
  await Promise.all([browser.open('https://www.bilibili.com/', []), browser.clear()])
  expect(fake.order).toEqual(['open', 'destroy'])
 })
})
