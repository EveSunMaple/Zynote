import {Button} from '@heroui/react'
import {useEffect, useLayoutEffect, useState} from 'react'
import {Copy, Minus, Square, X} from 'lucide-react'
import {invoke} from '@renderer/ui/UiParts'

/** 自绘标题栏：macOS 保留红绿灯只做拖拽区，Linux 提供最小化/最大化/关闭。
 * Windows 使用系统标题栏，渲染层不再绘制标题和窗口按钮。 */
export function TitleBar() {
 const platform = window.assistant.platform
 const [maximized, setMaximized] = useState(false)
 const usesCustomTitleBar = platform !== 'win32'
 useEffect(() => {
  if (!usesCustomTitleBar) return
  let active = true
  void invoke<boolean>('window:is-maximized').then(value => {
   if (active) setMaximized(value)
  })
  const off = window.assistant.onEvent(event => {
   if (event.kind === 'window') setMaximized(event.data.maximized)
  })
  return () => {
   active = false
   off()
  }
 }, [usesCustomTitleBar])
 useLayoutEffect(() => {
  document.documentElement.dataset.platform = platform
 }, [platform])
 useEffect(() => {
  document.documentElement.dataset.maximized = maximized ? 'true' : 'false'
 }, [maximized])
 const isMac = platform === 'darwin'
 if (!usesCustomTitleBar) return null
 return (
  <header
   className={'titlebar' + (isMac ? ' titlebar-mac' : '')}
   onDoubleClick={event => {
    if ((event.target as HTMLElement).closest('button')) return
    void invoke('window:toggle-maximize')
   }}
  >
   {!isMac && <span className="titlebar-title">摘云</span>}
   {!isMac && (
    <div className="titlebar-controls">
     <Button
      variant="ghost"
      size="sm"
      isIconOnly
      type="button"
      aria-label="最小化"
      onClick={() => void invoke('window:minimize')}
     >
      <Minus size={15} aria-hidden="true" />
     </Button>
     <Button
      variant="ghost"
      size="sm"
      isIconOnly
      type="button"
      aria-label={maximized ? '还原窗口' : '最大化'}
      onClick={() => void invoke('window:toggle-maximize')}
     >
      {maximized ? <Copy size={13} aria-hidden="true" /> : <Square size={13} aria-hidden="true" />}
     </Button>
     <Button
      variant="ghost"
      size="sm"
      isIconOnly
      type="button"
      className="titlebar-close"
      aria-label="关闭窗口"
      onClick={() => void invoke('window:close')}
     >
      <X size={15} aria-hidden="true" />
     </Button>
    </div>
   )}
  </header>
 )
}
