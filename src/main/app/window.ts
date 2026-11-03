import {BrowserWindow} from 'electron'
import {join} from 'node:path'

/** 主窗口：Windows 使用原生窗口按钮叠加层，标题栏底色跟随应用主题；
 * Linux 无边框自绘标题栏；macOS 隐藏标题栏但保留红绿灯。
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
  ...(process.platform === 'darwin'
   ? {titleBarStyle: 'hiddenInset' as const}
   : process.platform === 'win32'
     ? {
        titleBarStyle: 'hidden' as const,
        // 与 styles/tailwind.css 的 --titlebar-height 保持一致；透明底色让渲染层标题栏跟随应用主题。
        titleBarOverlay: {color: '#00000000', symbolColor: '#8b8b8b', height: 36}
       }
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
