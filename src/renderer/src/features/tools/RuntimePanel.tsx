import {Button} from '@heroui/react'
import {useEffect, useState, useRef} from 'react'
import {notify} from '@renderer/ui/ToastHost'
import type {RuntimeStatus} from '@shared/runtime'
export function RuntimePanel() {
 const [data, setData] = useState<RuntimeStatus>(),
  [busy, setBusy] = useState(false)
 const [refreshing, setRefreshing] = useState(false),
  generation = useRef(0)
 const [platform, setPlatform] = useState<string>()
 const isWindows = platform === 'win32'
 useEffect(() => {
  void window.assistant
   .invoke<{platform: string}>('app:info')
   .then(info => setPlatform(info.platform))
   .catch(() => setPlatform(''))
 }, [])
 const refresh = async () => {
  const current = ++generation.current
  setRefreshing(true)
  try {
   const result = await window.assistant.invoke<RuntimeStatus>('runtime:status')
   if (current === generation.current) {
    setData(result)
   }
  } catch (error) {
   if (current === generation.current) notify.error(error, {title: '读取组件状态失败'})
  } finally {
   if (current === generation.current) setRefreshing(false)
  }
 }
 useEffect(() => {
  void refresh()
  return () => {
   generation.current++
  }
 }, [])
 async function install(id: string) {
  if (data?.schema !== 2 && !isWindows) {
   notify.warning('运行组件目前仅支持 Windows，无法在当前平台安装。')
   return
  }
  setBusy(true)
  try {
   const result = await window.assistant.invoke('runtime:install', {id})
   if (result) {
    await refresh()
    notify.success('组件已校验并安装，请重启软件后使用。')
   }
  } catch (e) {
   notify.error(e, {title: '安装组件失败'})
  } finally {
   setBusy(false)
  }
 }
 const validationLabel = (component: RuntimeStatus['components'][number]) => {
  if (component.validation === 'verified') return '已校验'
  if (component.validation === 'invalid') return '校验失败'
  if (component.validation === 'unsupported_target') return '目标不支持'
  if (component.validation === 'missing') return '文件缺失'
  return component.installed ? '已安装' : '未安装'
 }

 return (
  <section aria-label="运行组件">
   <h3>运行组件</h3>
   <p>Zynote {data?.version || ''}</p>
   <p className="hint">完整包可离线使用。轻量包可选择同版本组件文件夹安装；不会自动下载或更改你的资料。</p>
   {platform !== undefined && !isWindows && data && data.schema !== 2 && (
    <p // NOSONAR: existing element/role semantics are intentional for layout and keyboard behavior.
     className="hint"
     role="status" // NOSONAR: styled live region; native output would change layout and announcement behavior.
    >
     运行组件目前仅支持 Windows；此平台不可安装 Windows 专属组件。
    </p>
   )}
   {data?.reason && (
    <p // NOSONAR: existing element/role semantics are intentional for layout and keyboard behavior.
     className="hint"
     role="status" // NOSONAR: styled live region; native output would change layout and announcement behavior.
    >
     {data.reason}
    </p>
   )}
   {data?.components.map(c => (
    <article className="runtime-component" key={c.id}>
     <div className="runtime-component__copy">
      <strong>{c.label}</strong>
      <span>
       {(c.bytes / 1048576).toFixed(1)} MiB · {validationLabel(c)}
      </span>
      {c.reason && <p className="hint">{c.reason}</p>}
     </div>
     <Button
      variant="tertiary"
      size="sm"
      isDisabled={
       busy || refreshing || c.installed || (data?.schema === 2 ? c.installable === false : platform !== 'win32')
      }
      onClick={() => void install(c.id)}
     >
      选择组件文件夹安装
     </Button>
    </article>
   ))}
   <Button
    className="runtime-refresh"
    variant="tertiary"
    size="sm"
    isDisabled={busy || refreshing}
    onClick={() => void refresh()}
   >
    {refreshing ? '正在校验组件…' : '刷新组件状态'}
   </Button>
  </section>
 )
}
