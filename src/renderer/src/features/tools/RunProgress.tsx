import {Spinner} from '@heroui/react'

export type RunProgressValue = {
 stage: string
 label: string
 chars?: number
 preview?: string
 elapsed_seconds: number
}

function duration(seconds: number) {
 if (seconds < 60) return `${seconds} 秒`
 return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
}

// 研究在后台运行时显示当前阶段、用时，以及模型正在写的末尾一段文字。
export function RunProgress({progress}: Readonly<{progress?: RunProgressValue}>) {
 if (!progress) return null
 const preview = progress.preview?.trim()
 return (
  <section className="research-progress" aria-live="polite" aria-label="研究进度">
   <div className="research-progress-head">
    <Spinner size="sm" />
    <strong>{progress.label}</strong>
    <span>
     已用 {duration(progress.elapsed_seconds)}
     {progress.chars ? ` · ${progress.stage === 'thinking' ? '已思考' : '已写'} ${progress.chars} 字` : ''}
    </span>
   </div>
   {preview && <p className="research-progress-preview">…{preview.slice(-240)}</p>}
  </section>
 )
}
