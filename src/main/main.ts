import {BiliBrowser} from '@main/media/bilibili/browser'
import {GoCore, nativeBinaryPath} from '@main/backend/go-core'
import {createAssistantInvoke} from '@main/backend/assistant-ipc'
import {resolveDataPaths} from '@main/backend/portable-data'
import {createShellServices} from '@main/backend/shell-services'
import {createUpdater, type Updater} from '@main/app/updater'
import {snapanyDownload} from '@main/media/sources/snapany'
import {diagnostic} from '@main/app/diagnostics'
import {createMainWindow} from '@main/app/window'
import {ZhiyunBrowser} from '@main/zhiyun/zhiyun-browser'
import {app, BrowserWindow, dialog, ipcMain, shell, protocol} from 'electron'
import {existsSync} from 'node:fs'
import {join, dirname} from 'node:path'
import {fileURLToPath} from 'node:url'
import {z} from 'zod'
import {ManagedPlayback} from '@main/media/managed-playback'
import {installMediaProtocol} from '@main/media/media-protocol'
import {playbackCloseSchema} from '@shared/managed-playback'

const mainDir = fileURLToPath(new URL('.', import.meta.url))
let mainWindow: BrowserWindow | undefined
let shutdownBackends: (() => Promise<void>) | undefined,
 quitting = false

function showMainWindow() {
 if (mainWindow && !mainWindow.isDestroyed()) {
  if (mainWindow.isMinimized()) mainWindow.restore()
  mainWindow.show()
  mainWindow.focus()
 }
}

protocol.registerSchemesAsPrivileged([
 {
  scheme: 'lecture-media',
  privileges: {standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true}
 }
])
app.setPath('userData', process.env.LISTENING_ASSISTANT_DATA || join(app.getPath('appData'), 'listening-assistant'))
if (!app.requestSingleInstanceLock()) app.quit()
else app.whenReady().then(startApplication).catch(showStartupError)

function showStartupError(error: unknown) {
 console.error('[startup]', error)
 dialog.showErrorBox('Zynote 启动失败', error instanceof Error ? error.message : '未知错误')
 app.quit()
}

const unhandled = Symbol('unhandled')

async function invokeShellAction(
 window: BrowserWindow,
 libraryRoot: string,
 updater: Updater,
 playback: ManagedPlayback,
 action: string,
 object: Record<string, unknown>
) {
 switch (action) {
  case 'managed-media:release':
   return playback.release(object)
  case 'managed-media:open':
   return playback.open(object)
  case 'managed-media:close':
   playback.close(playbackCloseSchema.parse(object).requestID)
   return null
  case 'update:status':
   return updater.status()
  case 'update:check':
   return updater.check()
  case 'update:download':
   return updater.download()
  case 'update:install':
   return updater.install()
  case 'window:minimize':
   window.minimize()
   return null
  case 'window:toggle-maximize':
   if (window.isMaximized()) window.unmaximize()
   else window.maximize()
   return window.isMaximized()
  case 'window:close':
   window.close()
   return null
  case 'window:is-maximized':
   return window.isMaximized()
  case 'external': {
   const url = z.string().url().parse(object.url)
   if (!['http:', 'https:'].includes(new URL(url).protocol)) throw new Error('仅支持打开网页链接')
   await shell.openExternal(url)
   return true
  }
  case 'pick': {
   const kind = z.enum(['transcript', 'document', 'srt', 'courseware']).parse(object.kind)
   const filters: Record<string, string[]> = {
    transcript: ['docx', 'txt', 'md', 'srt'],
    document: ['pdf', 'docx', 'txt', 'md'],
    srt: ['srt'],
    courseware: ['ppt', 'pptx', 'pdf', 'docx', 'txt', 'md']
   }
   const picked = await dialog.showOpenDialog(window, {
    title: kind === 'srt' ? '选择字幕文件' : '选择资料文件',
    defaultPath: libraryRoot,
    properties: kind === 'srt' || kind === 'transcript' ? ['openFile'] : ['openFile', 'multiSelections'],
    filters: [{name: kind === 'transcript' ? '语音识别原文' : '资料', extensions: filters[kind]}]
   })
   return picked.canceled ? [] : picked.filePaths
  }
  case 'video-ppt:pick': {
   const picked = await dialog.showOpenDialog(window, {
    title: '选择已下载的视频',
    properties: ['openFile'],
    filters: [{name: '视频', extensions: ['mp4', 'mkv', 'mov', 'webm']}]
   })
   return picked.canceled ? null : (picked.filePaths[0] ?? null)
  }
  case 'ppt-extract:pick': {
   const picked = await dialog.showOpenDialog(window, {
    title: '选择需要截取的 PPT',
    properties: ['openFile'],
    filters: [{name: '图片型 PowerPoint', extensions: ['pptx']}]
   })
   return picked.canceled ? null : (picked.filePaths[0] ?? null)
  }
  default:
   return unhandled
 }
}

async function waitForCore(goCore: GoCore) {
 const deadline = Date.now() + 15000
 while (goCore.currentStatus !== 'ready') {
  if (goCore.currentStatus === 'failed' || goCore.currentStatus === 'disposed') throw new Error('Go 后端不可用')
  if (Date.now() > deadline) throw new Error('Go 后端启动超时')
  await new Promise(resolve => setTimeout(resolve, 25))
 }
}

async function startApplication() {
 const root = app.getPath('userData'),
  appDirectory =
   process.env.LISTENING_ASSISTANT_PORTABLE_TEST || (app.isPackaged ? dirname(process.execPath) : app.getAppPath()),
  portable =
   !!process.env.LISTENING_ASSISTANT_PORTABLE_TEST ||
   process.env.LISTENING_ASSISTANT_PORTABLE === '1' ||
   existsSync(join(appDirectory, 'portable.flag'))
 diagnostic(root, 'application', 'startup', {code: app.getVersion()})
 const paths = await resolveDataPaths({
  root,
  appDirectory,
  legacyLibrary: join(app.getPath('documents'), '听课助手资料'),
  portable
 })
 const window = createMainWindow({
  mainDir,
  icon: join(app.isPackaged ? process.resourcesPath : join(app.getAppPath(), 'resources'), 'icon.png')
 })
 mainWindow = window
 window.on('close', event => {
  if (process.platform === 'darwin' && !quitting) {
   event.preventDefault()
   window.hide()
  }
 })
 window.on('maximize', () => window.webContents.send('assistant:event', {kind: 'window', data: {maximized: true}}))
 window.on('unmaximize', () => window.webContents.send('assistant:event', {kind: 'window', data: {maximized: false}}))
 const updater = createUpdater({
  onEvent: event => {
   if (!window.isDestroyed()) window.webContents.send('assistant:event', event)
  }
 })
 const shellServices = createShellServices(window, paths.libraryRoot)
 const biliBrowser = new BiliBrowser(window, event => {
  if (!window.isDestroyed()) window.webContents.send('assistant:event', event)
 })
 shellServices['shell.biliOpen'] = params => biliBrowser.open(z.string().parse(params.url), params.cookies)
 shellServices['shell.biliClear'] = () => biliBrowser.clear()

 const zhiyunBrowser = new ZhiyunBrowser(window, message =>
  window.webContents.send('assistant:event', {
   kind: 'progress',
   data: {stage: '智云登录', message, error: true}
  })
 )
 shellServices['zhiyun.login'] = async () => {
  await zhiyunBrowser.home()
  return true
 }
 shellServices['zhiyun.refresh'] = () => {
  zhiyunBrowser.refresh()
  return true
 }
 shellServices['zhiyun.back'] = () => {
  zhiyunBrowser.back()
  return true
 }
 shellServices['zhiyun.bounds'] = params => {
  const bounds = z
   .object({
    x: z.number().min(0).max(10000),
    y: z.number().min(0).max(10000),
    width: z.number().min(0).max(10000),
    height: z.number().min(0).max(10000)
   })
   .parse(params.bounds)
  zhiyunBrowser.bounds(bounds, z.boolean().parse(params.visible))
  return true
 }
 shellServices['shell.snapanyDownload'] = async params => {
  const url = z.string().min(1).parse(params.url)
  const path = z.string().min(1).max(4096).parse(params.path)
  await snapanyDownload(window, url, path, () => {}, AbortSignal.timeout(30 * 60 * 1000))
  return true
 }
 shellServices['tools.configure'] = async () => true
 let playback: ManagedPlayback | undefined
 const goCore = new GoCore({
  appVersion: app.getVersion(),
  dataRoot: paths.dataRoot,
  configRoot: root,
  appRoot: appDirectory,
  libraryRoot: paths.libraryRoot,
  previousLibraryRoot: paths.previousLibraryRoot,
  resourcesRoot: app.isPackaged ? process.resourcesPath : join(app.getAppPath(), 'resources'),
  promptsRoot: app.isPackaged ? process.resourcesPath : join(app.getAppPath(), 'src'),
  nativePath: nativeBinaryPath(),
  services: shellServices,
  portable: paths.portable,
  onEvent: e => {
   if (!window.isDestroyed()) window.webContents.send('assistant:event', e)
  },
  onUnavailable: () => playback?.closeAll(),
  onFailed: reason => {
   diagnostic(root, 'go-core', 'backend-failed', {code: 'START_FAILED'})
   console.error('[go-core]', reason)
  }
 })
 goCore.start()
 await waitForCore(goCore)
 await goCore.call('db:bootstrap', {})
 playback = new ManagedPlayback(
  paths.libraryRoot,
  (id, expectedSourceSHA256) => goCore.call('video-ppt:playback-source', {id, expectedSourceSHA256}),
  (action, input) => goCore.call(action, input)
 )
 const media = playback
 const removeMedia = installMediaProtocol(
  window.webContents.session,
  window,
  media,
  process.env.ELECTRON_RENDERER_URL ? new URL(process.env.ELECTRON_RENDERER_URL).origin : undefined
 )
 ipcMain.handle(
  'assistant:invoke',
  createAssistantInvoke(
   window,
   async (action, object) => {
    const shellResult = await invokeShellAction(window, paths.libraryRoot, updater, media, action, object)
    if (shellResult !== unhandled) return shellResult
    await waitForCore(goCore)
    return await goCore.call(action, object)
   },
   (action, failure) => {
    diagnostic(root, 'ipc', 'request-failed', {action, code: failure.code, message: failure.message})
   }
  )
 )
 if (process.env.ELECTRON_RENDERER_URL) await window.loadURL(process.env.ELECTRON_RENDERER_URL)
 else await window.loadFile(join(mainDir, '../renderer/index.html'))
 showMainWindow()
 shutdownBackends = async () => {
  await removeMedia()
  updater.dispose()
  zhiyunBrowser.dispose()
  biliBrowser.dispose()
  goCore.dispose()
 }
}

app.on('before-quit', event => {
 if (quitting) return
 quitting = true
 if (shutdownBackends) {
  event.preventDefault()
  void shutdownBackends().finally(() => app.quit())
 }
})
app.on('window-all-closed', () => {
 if (process.platform !== 'darwin') app.quit()
})
app.on('second-instance', showMainWindow)
app.on('activate', showMainWindow)
