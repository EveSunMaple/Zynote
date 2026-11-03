import {BrowserWindow} from 'electron'
import {join} from 'node:path'

/** 主窗口：Windows 使用系统标题栏；Linux 无边框自绘标题栏；macOS 隐藏标题栏但保留红绿灯。
 * Linux 使用透明窗口，由渲染层 CSS 提供与系统一致的圆角。 */
export function createMainWindow({mainDir, icon}: {mainDir: string; icon: string}) {
 const window = new BrowserWindow({
  width: 1500,
  height: 950,
  minWidth: 1050,
  minHeight: 700,
  title: '摘云',
  icon,
  backgroundColor: process.platform === 'linux' ? '#00000000' : '#f9fafb',
  autoHideMenuBar: process.platform === 'win32',
  ...(process.platform === 'darwin'
   ? {titleBarStyle: 'hiddenInset' as const}
   : process.platform === 'win32'
     ? {frame: true}
     : {frame: false, ...(process.platform === 'linux' ? {transparent: true} : {})}),
  webPreferences: {
   preload: join(mainDir, '../preload/preload.cjs'),
   contextIsolation: true,
   nodeIntegration: false,
   sandbox: true
  }
 })
 if (process.platform === 'win32') window.setMenu(null)
 window.webContents.setWindowOpenHandler(() => ({action: 'deny'}))
 window.webContents.on('will-navigate', e => e.preventDefault())
 return window
}
