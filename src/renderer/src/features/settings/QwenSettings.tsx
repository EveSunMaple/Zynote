import {Button, Input} from '@heroui/react'
import {useEffect, useRef, useState} from 'react'
import {SelectField} from '@renderer/ui/SelectField'
import {notify} from '@renderer/ui/ToastHost'
import type {Profile, Settings} from '@shared/types'
const invoke = <T,>(action: string, args?: unknown) => window.assistant.invoke<T>(action, args)
const fallback: Profile = {
 id: 'qwen-default',
 name: '通义千问',
 provider: 'qwen',
 baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
 model: '',
 visionModel: '',
 apiFormat: 'chat_completions',
 models: []
}
const jobRunning = (error: unknown) => /当前已有任务运行/.test(error instanceof Error ? error.message : String(error))

// 模型读取和测试共用后台任务槽。其他任务运行时只查任务状态，避免反复禁用表单。
function whenJobsIdle<T>(
 delay: number,
 request: () => Promise<T>,
 waiting: () => void,
 done: (result: T) => void,
 failed: (error: unknown) => void
) {
 let active = true
 let timer = window.setTimeout(check, delay)
 function wait() {
  waiting()
  timer = window.setTimeout(check, 1500)
 }
 async function check() {
  try {
   const job = await invoke('job:current')
   if (!active) return
   if (job) return wait()
   const result = await request()
   if (active) done(result)
  } catch (error) {
   if (!active) return
   if (jobRunning(error)) wait()
   else failed(error)
  }
 }
 return () => {
  active = false
  window.clearTimeout(timer)
 }
}

export function QwenSettings({
 value,
 update,
 save,
 busy
}: Readonly<{
 value: Settings
 update: (patch: Partial<Settings>) => void
 save: () => void
 busy: boolean
}>) {
 const [notice, setNotice] = useState(''),
  [manualText, setManualText] = useState(''),
  [manualVision, setManualVision] = useState(''),
  [reading, setReading] = useState(false),
  [available, setAvailable] = useState<boolean | null>(null)
 const profile = value.profiles.find(p => p.id === value.activeProfile) || value.profiles[0] || fallback
 const latestProfile = useRef(profile)
 latestProfile.current = profile
 useEffect(() => {
  if (!value.profiles.length) update({profiles: [fallback], activeProfile: fallback.id, connection: 'qwen'})
 }, [])
 const patch = (fields: Partial<Profile>) =>
  update({
   connection: 'qwen',
   activeProfile: latestProfile.current.id,
   profiles: [{...latestProfile.current, ...fields, provider: 'qwen', apiFormat: 'chat_completions'}]
  })
 const disabled = busy || reading,
  hasKey = !!(profile.apiKey?.trim() || profile.hasKey),
  unavailable = available !== true
 let textPlaceholder = '请选择文本模型'
 if (reading) textPlaceholder = '正在读取可用模型…'
 else if (unavailable) textPlaceholder = '暂无可用的文本模型'
 let visionPlaceholder = '请选择视觉模型'
 if (reading) visionPlaceholder = '正在读取可用模型…'
 else if (unavailable) visionPlaceholder = '暂无可用的视觉模型'
 // 自动读取可用模型：进入页面、地址或密钥变化时重新读取；失败即视为 API 不可用。
 useEffect(() => {
  setReading(false)
  setAvailable(false)
  if (!hasKey) {
   setNotice('填写并保存通义千问 API Key 后自动读取可用模型')
   return
  }
  const waiting = () => {
   setReading(false)
   setNotice('任务运行中，稍后自动读取模型')
  }
  if (busy) return waiting()
  return whenJobsIdle(
   700,
   () => {
    setReading(true)
    setNotice('正在读取可用模型…')
    return invoke<string[]>('settings:models', {profile: latestProfile.current})
   },
   waiting,
   models => {
    const added = (latestProfile.current.models || []).filter(model => !profile.models?.includes(model))
    patch({models: [...new Set([...models, ...added])]})
    setReading(false)
    setAvailable(true)
    setNotice(`已自动读取 ${models.length} 个可用模型`)
   },
   error => {
    setReading(false)
    setNotice(error instanceof Error ? error.message : String(error))
    notify.error(error, {title: '读取可用模型失败'})
   }
  )
 }, [profile.baseUrl, profile.apiKey, profile.hasKey, profile.id, busy])
 // 自动测试连接：读取成功后，所选文本模型或连接信息变化时静默测试。
 useEffect(() => {
  if (busy || !available || !hasKey) return
  if (!profile.model) {
   setNotice(`已自动读取 ${profile.models?.length ?? 0} 个可用模型`)
   return
  }
  return whenJobsIdle(
   500,
   () => {
    setNotice('正在自动测试连接…')
    return invoke<string>('settings:probe', {profile: latestProfile.current})
   },
   () => setNotice('任务运行中，稍后自动测试连接'),
   reply => setNotice('连接测试成功：' + reply),
   error => {
    setNotice('连接测试失败：' + (error instanceof Error ? error.message : String(error)))
    notify.error(error, {title: '连接测试失败'})
   }
  )
 }, [profile.model, profile.baseUrl, profile.apiKey, profile.hasKey, profile.id, available, busy])
 const known = [...new Set(profile.models || [])]
 // qwen3.8-flash 支持图像输入，但模型名不含 VL。
 const visionCandidates = [
  ...new Set([
   profile.visionModel || '',
   ...(profile.models || []).filter(model => model === 'qwen3.8-flash' || /(?:^|[-_/.])vl(?:$|[-_/.\d])/i.test(model))
  ])
 ]
 return (
  <div className="panel-section">
   <header>
    <div>
     <h2>通义千问连接</h2>
     <p className="hint">模型通过 DashScope OpenAI 兼容 API 直连。旧登录凭据不会迁移到此连接。</p>
    </div>
    <div className="row">
     <Button
      variant="tertiary"
      size="sm"
      isDisabled={disabled}
      onClick={() => void invoke('external', {url: 'https://bailian.console.aliyun.com/'})}
     >
      获取通义千问 API Key
     </Button>
     <Button variant="primary" size="sm" isDisabled={disabled} onClick={save}>
      保存模型设置
     </Button>
    </div>
   </header>
   <label>
    API 地址
    <Input
     aria-label="通义千问 API 地址"
     disabled={disabled}
     value={profile.baseUrl}
     onChange={e => patch({baseUrl: e.target.value})}
    />
    <small className="hint">
     使用业务空间密钥时，从百炼控制台复制该空间的 API 地址，保留末尾的 /compatible-mode/v1。
    </small>
   </label>
   <label>
    通义千问 API Key
    <Input
     aria-label="通义千问 API Key"
     type="password"
     autoComplete="off"
     spellCheck={false}
     disabled={disabled}
     value={profile.apiKey || ''}
     placeholder={profile.hasKey ? '已加密保存，留空保持原密钥' : '填写通义千问 API Key'}
     onChange={e => patch({apiKey: e.target.value || undefined})}
    />
   </label>
   <label>
    文本模型
    <SelectField
     label="文本模型"
     placeholder={textPlaceholder}
     isDisabled={disabled || unavailable}
     value={profile.model}
     onChange={next => patch({model: next})}
     options={[
      {value: '', label: '请选择文本模型'},
      ...[...new Set([profile.model, ...known])].filter(Boolean).map(model => ({value: model, label: model}))
     ]}
    />
   </label>
   <div className="row">
    <Input
     aria-label="自定义文本模型 ID"
     disabled={disabled}
     value={manualText}
     placeholder="自定义文本模型 ID"
     onChange={e => setManualText(e.target.value)}
    />
    <Button
     variant="tertiary"
     size="sm"
     isDisabled={disabled || !manualText.trim()}
     onClick={() => {
      const model = manualText.trim()
      patch({model, models: [...new Set([...(profile.models || []), model])]})
      setManualText('')
     }}
    >
     添加文本模型
    </Button>
   </div>
   <label>
    视觉模型
    <SelectField
     label="视觉模型"
     placeholder={visionPlaceholder}
     isDisabled={disabled || unavailable}
     value={profile.visionModel || ''}
     onChange={next => patch({visionModel: next})}
     options={[
      {value: '', label: '请选择视觉模型'},
      ...visionCandidates.filter(Boolean).map(model => ({value: model, label: model}))
     ]}
    />
   </label>
   <div className="row">
    <Input
     aria-label="自定义视觉模型 ID"
     disabled={disabled}
     value={manualVision}
     placeholder="自定义视觉模型 ID"
     onChange={e => setManualVision(e.target.value)}
    />
    <Button
     variant="tertiary"
     size="sm"
     isDisabled={disabled || !manualVision.trim()}
     onClick={() => {
      const model = manualVision.trim()
      patch({visionModel: model, models: [...new Set([...(profile.models || []), model])]})
      setManualVision('')
     }}
    >
     添加视觉模型
    </Button>
   </div>
   <p className="hint">
    课表截图等图片任务使用视觉模型。可从列表选择 qwen3.8-flash 或 VL 模型，也可手动填写支持图像输入的模型
    ID。模型调用会将字幕、图片和课程资料发送到你配置的服务端。
   </p>
   {notice && (
    <p // NOSONAR: existing element/role semantics are intentional for layout and keyboard behavior.
     role="status" // NOSONAR: styled live region; native output would change layout and announcement behavior.
     className="notice"
    >
     {notice}
    </p>
   )}
  </div>
 )
}
