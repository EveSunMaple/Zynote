import {Button, Card} from '@heroui/react'
import {useEffect, useState} from 'react'
import {invoke} from '@renderer/ui/UiParts'
import {notify} from '@renderer/ui/ToastHost'
import type {UpdateStatus} from '@shared/update'

const labels: Record<UpdateStatus['phase'], string> = {
 disabled: '此版本不检查更新',
 idle: '等待检查',
 checking: '正在检查更新',
 available: '发现新版本，正在后台下载',
 downloading: '正在下载更新',
 downloaded: '更新已下载，重启后安装',
 'not-available': '当前已是最新版本',
 error: '检查更新失败'
}

function description(status: UpdateStatus) {
 if (status.phase === 'downloading' && status.percent !== undefined) return `已完成 ${status.percent.toFixed(1)}%`
 if (status.phase === 'available' && status.version) return `新版本 ${status.version}`
 if (status.phase === 'downloaded' && status.version) return `新版本 ${status.version} 已准备好`
 if (status.message) return status.message
 return labels[status.phase]
}

export function UpdatePanel() {
 const [status, setStatus] = useState<UpdateStatus>()
 const [busy, setBusy] = useState(false)
 useEffect(() => {
  void invoke<UpdateStatus>('update:status')
   .then(setStatus)
   .catch(reason => notify.error(reason, {title: '读取更新状态失败'}))
  return window.assistant.onEvent(event => {
   if (event.kind === 'update') setStatus(event.data)
  })
 }, [])
 async function check() {
  setBusy(true)
  try {
   setStatus(await invoke<UpdateStatus>('update:check'))
  } catch (reason) {
   notify.error(reason, {title: '检查更新失败'})
  } finally {
   setBusy(false)
  }
 }
 async function install() {
  setBusy(true)
  try {
   await invoke('update:install')
  } catch (reason) {
   notify.error(reason, {title: '安装更新失败'})
  } finally {
   setBusy(false)
  }
 }
 if (!status)
  return (
   <div className="panel-section">
    <Card className="knowledge-card">
     <p className="hint">正在读取更新状态…</p>
    </Card>
   </div>
  )
 return (
  <div className="panel-section">
   <Card className="knowledge-card">
    <h3>应用更新</h3>
    <div className="row">
     <span>当前版本</span>
     <span>{status.currentVersion}</span>
    </div>
    <div className="row">
     <span>状态</span>
     <span>{description(status)}</span>
    </div>
    {status.phase === 'downloading' && status.percent !== undefined && (
     <progress className="update-progress" max={100} value={status.percent} aria-label="更新下载进度" />
    )}
    {status.releaseNotes && (
     <div>
      <strong>更新说明</strong>
      <p className="update-release-notes">{status.releaseNotes}</p>
     </div>
    )}
    <p className="hint">更新从 GitHub Releases 获取；下载完成后需重启应用安装。</p>
    <div className="row">
     <Button variant="tertiary" size="sm" isDisabled={busy || status.phase === 'disabled'} onPress={check}>
      检查更新
     </Button>
     {status.phase === 'downloaded' && (
      <Button variant="primary" size="sm" isDisabled={busy} onPress={install}>
       重启并安装
      </Button>
     )}
    </div>
   </Card>
  </div>
 )
}
