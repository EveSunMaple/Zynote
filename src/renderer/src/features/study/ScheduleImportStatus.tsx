import {useEffect, useState} from 'react'
import {Button} from '@heroui/react'
import type {StudyImportProgress} from '@shared/types'
import {StudyNextStep} from '@renderer/features/study/StudyNextStep'
import {errorMessage} from '@renderer/ui/ToastHost'
import './schedule-import-status.css'

type Phase = 'choosing' | StudyImportProgress['phase']
export interface ScheduleImportState {
 requestId: string
 phase: Phase
 fileName: string
 startedAt: number
 finishedAt?: number
 count?: number
 saved?: boolean
 error?: string
 sent: boolean
 retrying?: boolean
}

const phases: Record<Phase, {step: number; label: string}> = {
 choosing: {step: 0, label: '等待选择图片'},
 reading: {step: 0, label: '已选中图片，正在读取'},
 ready: {step: 0, label: '图片已读取，正在准备识别'},
 uploading: {step: 1, label: '正在发送图片给千问'},
 sent: {step: 2, label: '图片已发送，正在等待千问返回'},
 received: {step: 2, label: '已收到千问响应，正在读取识别结果'},
 retrying: {step: 2, label: '千问返回的课表格式有误，正在重试'}
}

export function useScheduleImportStatus() {
 const [status, setStatus] = useState<ScheduleImportState | null>(null)
 useEffect(
  () =>
   window.assistant.onEvent(event => {
    if (event.kind !== 'study-import') return
    const {requestId, phase, fileName} = event.data
    setStatus(current => {
     if (!current || current.requestId !== requestId || current.finishedAt !== undefined) return current
     return {
      ...current,
      phase,
      fileName,
      // Measure processing time from file selection rather than time spent in the chooser.
      startedAt: current.phase === 'choosing' ? Date.now() : current.startedAt,
      sent: current.sent || phase === 'sent' || phase === 'received',
      retrying: current.retrying || phase === 'retrying'
     }
    })
   }),
  []
 )

 function begin() {
  const requestId = crypto.randomUUID()
  setStatus({requestId, phase: 'choosing', fileName: '', startedAt: Date.now(), sent: false})
  return requestId
 }
 function finish(requestId: string, count: number) {
  setStatus(current => (current?.requestId === requestId ? {...current, count, finishedAt: Date.now()} : current))
 }
 function cancel(requestId: string) {
  setStatus(current => (current?.requestId === requestId ? null : current))
 }
 function fail(requestId: string, error: unknown) {
  setStatus(current =>
   current?.requestId === requestId ? {...current, error: errorMessage(error), finishedAt: Date.now()} : current
  )
 }
 function markSaved() {
  setStatus(current => (current?.count !== undefined ? {...current, saved: true} : current))
 }
 function clear() {
  setStatus(null)
 }
 return {status, begin, finish, cancel, fail, markSaved, clear}
}

export function ScheduleImportStatus({
 status,
 onRetry,
 busy
}: Readonly<{status: ScheduleImportState | null; onRetry: () => void; busy: boolean}>) {
 const [now, setNow] = useState(Date.now)
 const timing = !!status && status.phase !== 'choosing' && status.finishedAt === undefined
 useEffect(() => {
  if (!timing) return
  const timer = window.setInterval(() => setNow(Date.now()), 1000)
  return () => window.clearInterval(timer)
 }, [timing])
 if (!status || status.saved) return null
 const {step, label} = phases[status.phase]
 const seconds = Math.max(0, Math.floor(((status.finishedAt ?? now) - status.startedAt) / 1000))
 if (status.count !== undefined)
  return (
   <section className="schedule-import-summary" aria-label="截图识别进度">
    <span role="status">已识别 {status.count} 条课次</span>
    {status.fileName && (
     <span className="study-import-status__file" title={status.fileName}>
      {status.fileName}
     </span>
    )}
    <small>用时 {seconds} 秒</small>
   </section>
  )
 let message = status.retrying && status.phase !== 'retrying' ? `第二次识别：${label}` : label
 if (status.error) message = `${status.sent ? '图片已发送，识别失败' : '图片导入失败'}：${status.error}`
 return (
  <section className="study-import-status" aria-label="截图识别进度">
   <div className="study-import-status__heading">
    <strong>截图识别</strong>
    {status.phase !== 'choosing' && <small role="timer">用时 {seconds} 秒</small>}
   </div>
   {status.fileName && (
    <span className="study-import-status__file" title={status.fileName}>
     {status.fileName}
    </span>
   )}
   <p className="study-import-status__message" data-error={!!status.error} role="status" aria-live="polite">
    {message}
   </p>
   {timing && step >= 1 && <small>从开始发送起最多等待 120 秒，包含重试。</small>}
   {status.error && (
    <>
     {!busy && <StudyNextStep>点「重新上传图片」，重新选择课表截图。</StudyNextStep>}
     <Button size="sm" variant="primary" isDisabled={busy} onPress={onRetry}>
      重新上传图片
     </Button>
    </>
   )}
  </section>
 )
}
