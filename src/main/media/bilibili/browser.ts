import {BrowserWindow, Menu, session} from 'electron'
import type {EventPayload} from '@shared/types'

export function biliPage(value: string): URL {
 const url = new URL(value)
 if (
  url.protocol !== 'https:' ||
  url.username ||
  url.password ||
  (url.port && url.port !== '443') ||
  !(url.hostname === 'bilibili.com' || url.hostname.endsWith('.bilibili.com'))
 )
  throw new Error('仅允许 B站官方网页')
 return url
}
export function biliImport(value: string): string {
 const url = biliPage(value)
 if (
  !['bilibili.com', 'www.bilibili.com'].includes(url.hostname) ||
  !/^\/video\/(BV[A-Za-z0-9]{10}|av\d+)\/?$/.test(url.pathname)
 )
  throw new Error('请先打开具体视频页面')
 const page = url.searchParams.get('p') || '1'
 if (!/^[1-9]\d{0,4}$/.test(page)) throw new Error('视频分P无效')
 return `https://www.bilibili.com${url.pathname}?p=${page}`
}
export class BiliBrowser {
 private disposed = false
 private window?: BrowserWindow
 private pending: Promise<unknown> = Promise.resolve()
 private enqueue<T>(job: () => Promise<T>): Promise<T> {
  const next = this.pending.then(job)
  this.pending = next.catch(() => {})
  return next
 }
 constructor(
  private readonly owner: BrowserWindow,
  private readonly send: (event: EventPayload) => void
 ) {}
 clear() {
  return this.enqueue(() => this.clearSession())
 }
 private async clearSession() {
  this.window?.destroy()
  this.window = undefined
  await session.fromPartition('persist:zynote-bilibili').clearStorageData()
 }
 open(value: string, cookies: unknown) {
  return this.enqueue(() => this.openSession(value, cookies))
 }
 private async openSession(value: string, cookies: unknown) {
  if (this.disposed) throw new Error('B站浏览已关闭')
  const target = biliPage(value).href
  if (!Array.isArray(cookies) || cookies.length > 2000) throw new Error('登录会话无效')
  await this.clearSession()
  const browserSession = session.fromPartition('persist:zynote-bilibili')
  for (const row of cookies) {
   if (
    !row ||
    typeof row.name !== 'string' ||
    typeof row.value !== 'string' ||
    typeof row.domain !== 'string' ||
    !biliPage('https://' + row.domain).hostname ||
    typeof row.path !== 'string' ||
    !row.path.startsWith('/')
   )
    throw new Error('登录会话无效')
   if (row.expires && row.expires <= Date.now() / 1000) continue
   await browserSession.cookies.set({
    url: 'https://' + row.domain + row.path,
    name: row.name,
    value: row.value,
    ...(row.hostOnly ? {} : {domain: '.' + row.domain}),
    path: row.path,
    secure: !!row.secure,
    httpOnly: !!row.httpOnly,
    ...(row.expires > 0 ? {expirationDate: row.expires} : {}),
    sameSite:
     ({1: 'unspecified', 2: 'lax', 3: 'strict', 4: 'no_restriction'} as const)[row.sameSite as 1 | 2 | 3 | 4] ||
     'unspecified'
   })
  }
  await browserSession.cookies.flushStore()
  if (this.disposed) throw new Error('B站浏览已关闭')
  const window = new BrowserWindow({
   width: 1250,
   height: 850,
   title: 'Zynote · B站浏览',
   webPreferences: {
    session: browserSession,
    nodeIntegration: false,
    contextIsolation: true,
    sandbox: true,
    webSecurity: true
   }
  })
  this.window = window
  const importVideo = (mode: 'videoNotes' | 'videoSubtitles' | 'videoPpt') => {
   try {
    const url = biliImport(window.webContents.getURL())
    this.send({kind: 'bili-import', url, mode})
    this.owner.show()
    this.owner.focus()
   } catch {
    window.setTitle('Zynote · 请先打开具体视频页面，再选择导入')
   }
  }
  window.setMenu(
   Menu.buildFromTemplate([
    {
     label: 'B站浏览',
     submenu: [
      {
       label: '返回',
       click: () => {
        if (window.webContents.navigationHistory.canGoBack()) window.webContents.navigationHistory.goBack()
       }
      },
      {
       label: '前进',
       click: () => {
        if (window.webContents.navigationHistory.canGoForward()) window.webContents.navigationHistory.goForward()
       }
      },
      {label: '刷新', click: () => window.webContents.reload()},
      {label: '导入当前视频到视频摘要', click: () => importVideo('videoNotes')},
      {label: '导入当前视频到字幕转写', click: () => importVideo('videoSubtitles')},
      {label: '导入当前视频到视频课件', click: () => importVideo('videoPpt')}
     ]
    }
   ])
  )
  window.webContents.on('context-menu', () => {
   Menu.buildFromTemplate([
    {label: '导入当前视频到视频摘要', click: () => importVideo('videoNotes')},
    {label: '导入当前视频到字幕转写', click: () => importVideo('videoSubtitles')},
    {label: '导入当前视频到视频课件', click: () => importVideo('videoPpt')},
    {role: 'reload'},
    {
     label: '返回',
     click: () => {
      if (window.webContents.navigationHistory.canGoBack()) window.webContents.navigationHistory.goBack()
     }
    }
   ]).popup({window})
  })
  const guard = (event: Electron.Event, url: string) => {
   try {
    biliPage(url)
   } catch {
    event.preventDefault()
   }
  }
  window.webContents.on('will-navigate', guard)
  window.webContents.on('will-redirect', guard)
  window.webContents.setWindowOpenHandler(({url}) => {
   try {
    const target = biliPage(url).href
    void window.loadURL(target).catch(() => window.setTitle('B站页面未能打开，请刷新重试'))
   } catch {}
   return {action: 'deny'}
  })
  window.on('closed', () => {
   if (this.window === window) this.window = undefined
  })
  await window.loadURL(target)
  return true
 }
 dispose() {
  this.disposed = true
  this.window?.destroy()
  this.window = undefined
 }
}
