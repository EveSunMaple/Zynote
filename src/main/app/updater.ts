import electronUpdater, {type ProgressInfo, type UpdateInfo} from 'electron-updater'
import {app} from 'electron'
import {existsSync} from 'node:fs'
import {join} from 'node:path'
import type {EventPayload} from '@shared/types'
import type {UpdateStatus} from '@shared/update'

const {autoUpdater} = electronUpdater

const CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000
const FIRST_CHECK_DELAY_MS = 20 * 1000

function releaseNotes(info: UpdateInfo) {
 if (typeof info.releaseNotes === 'string') return info.releaseNotes
 if (Array.isArray(info.releaseNotes))
  return info.releaseNotes
   .map(note => note.note)
   .filter((note): note is string => typeof note === 'string' && note.length > 0)
   .join('\n\n')
 return undefined
}

function errorMessage(error: unknown) {
 const message = error instanceof Error ? error.message : typeof error === 'string' ? error : ''
 if (/No published versions/i.test(message)) return '尚未发布可用的更新版本'
 if (/net::|ENOTFOUND|ECONNREFUSED|ETIMEDOUT/i.test(message)) return '无法连接更新服务，请检查网络后重试'
 return message || '检查更新失败'
}

export function createUpdater({onEvent}: {onEvent: (event: EventPayload) => void}) {
 const supported = app.isPackaged && existsSync(join(process.resourcesPath, 'app-update.yml'))
 let status: UpdateStatus = {
  phase: supported ? 'idle' : 'disabled',
  currentVersion: app.getVersion(),
  ...(supported ? {} : {message: '开发模式或便携版不检查更新'})
 }
 let checking = false,
  downloading = false
 autoUpdater.autoDownload = process.env.ZYNOTE_UPDATE_AUTODOWNLOAD !== '0'
 autoUpdater.autoInstallOnAppQuit = true
 autoUpdater.allowPrerelease = false
 autoUpdater.logger = null

 const publish = (patch: Partial<UpdateStatus>) => {
  status = {...status, ...patch}
  onEvent({kind: 'update', data: status})
 }

 const fail = (error: unknown) => {
  publish({phase: 'error', message: errorMessage(error)})
 }

 autoUpdater.on('checking-for-update', () => publish({phase: 'checking', message: undefined}))
 autoUpdater.on('update-available', info =>
  publish({
   phase: 'available',
   version: info.version,
   releaseDate: info.releaseDate,
   releaseNotes: releaseNotes(info),
   message: '发现新版本，正在后台下载'
  })
 )
 autoUpdater.on('update-not-available', info =>
  publish({
   phase: 'not-available',
   version: info.version,
   releaseDate: info.releaseDate,
   releaseNotes: releaseNotes(info),
   message: '当前已是最新版本'
  })
 )
 autoUpdater.on('download-progress', (progress: ProgressInfo) =>
  publish({
   phase: 'downloading',
   percent: progress.percent,
   transferred: progress.transferred,
   total: progress.total,
   bytesPerSecond: progress.bytesPerSecond,
   message: '正在下载更新'
  })
 )
 autoUpdater.on('update-downloaded', info =>
  publish({
   phase: 'downloaded',
   version: info.version,
   releaseDate: info.releaseDate,
   releaseNotes: releaseNotes(info),
   message: '更新已下载，重启后安装'
  })
 )
 autoUpdater.on('error', fail)

 async function check() {
  if (!supported || checking || downloading) return status
  checking = true
  try {
   await autoUpdater.checkForUpdates()
  } catch (error) {
   fail(error)
  } finally {
   checking = false
  }
  return status
 }

 async function download() {
  if (!supported || checking || downloading || status.phase !== 'available') return status
  downloading = true
  publish({phase: 'downloading', percent: 0, message: '正在下载更新'})
  try {
   await autoUpdater.downloadUpdate()
  } catch (error) {
   fail(error)
  } finally {
   downloading = false
  }
  return status
 }

 function install() {
  if (status.phase !== 'downloaded') return false
  setImmediate(() => autoUpdater.quitAndInstall(false, true))
  return true
 }

 let timer: NodeJS.Timeout | undefined
 let interval: NodeJS.Timeout | undefined
 if (supported) {
  timer = setTimeout(() => void check(), FIRST_CHECK_DELAY_MS)
  timer.unref?.()
  interval = setInterval(() => void check(), CHECK_INTERVAL_MS)
  interval.unref?.()
 }

 return {
  status: () => status,
  check,
  download,
  install,
  dispose: () => {
   if (timer) clearTimeout(timer)
   if (interval) clearInterval(interval)
   autoUpdater.removeAllListeners()
  }
 }
}

export type Updater = ReturnType<typeof createUpdater>
