import {Button} from '@heroui/react'
import {useEffect, useRef, useState} from 'react'
import {
 publicationAuditKey,
 publicationAuditMessage,
 PublicationAuditSession,
 type PublicationAuditTarget,
 type PublicationAuditResult
} from '@shared/publication-audit'

export function PublicationAudit({
 target,
 sourceHash
}: Readonly<{target?: PublicationAuditTarget; sourceHash?: string}>) {
 const session = useRef(new PublicationAuditSession())
 const [view, setView] = useState<{
  key: string
  pending: boolean
  result?: PublicationAuditResult
  error?: string
 } | null>(null)
 const key = target ? publicationAuditKey(target, sourceHash) : ''
 useEffect(() => {
  session.current.change(key)
  setView(current => (current && current.key !== key ? null : current))
 }, [key])
 useEffect(() => () => session.current.invalidate(), [])
 const current = view?.key === key ? view : null
 async function audit() {
  if (!target) return
  const token = session.current.start(key)
  if (!token) return
  setView({key, pending: true})
  try {
   const result = await window.assistant.invoke<PublicationAuditResult>('subtitle-derived:audit-publication', {
    id: target.id,
    itemIndex: target.itemIndex,
    expectedPlanSHA256: target.record?.planSHA256 || '',
    expectedReceiptSHA256: target.record?.receiptSHA256 || ''
   })
   if (!session.current.finish(token)) return
   const matches =
    result.taskID === target.id &&
    result.itemIndex === target.itemIndex &&
    result.planSHA256 === target.record?.planSHA256 &&
    result.receiptSHA256 === target.record?.receiptSHA256 &&
    result.mdPositionScope === target.record?.mdPositionScope &&
    (!sourceHash || result.sourceSHA256 === sourceHash)
   setView({
    key,
    pending: false,
    result:
     result.state === 'complete' && !matches
      ? {...result, state: 'unknown', reason: 'stale-record', checkedAt: undefined}
      : result
   })
  } catch {
   if (session.current.finish(token)) setView({key, pending: false, error: '本次核验未完成，请稍后重试。'})
  }
 }
 return (
  <section className="publication-audit" aria-label="成果引用核验">
   <p className="hint">
    {target?.record?.state === 'recorded'
     ? '保存时已有成果凭据；当前文件尚需按需核验。'
     : target?.record?.state === 'pending'
       ? '已有发布计划，尚无完成凭据。'
       : '暂无可用成果凭据声明。'}
   </p>
   {target && (
    <Button variant="tertiary" size="sm" isDisabled={!!current?.pending || !sourceHash} onClick={() => void audit()}>
     {current?.pending ? '正在核验…' : '核验成果引用'}
    </Button>
   )}
   {current?.result && (
    <p className="hint" role="status">
     {publicationAuditMessage(current.result)}
    </p>
   )}
   {current?.error && (
    <p className="hint" role="alert">
     {current.error}
    </p>
   )}
   <p className="hint">
    核验是读取当时的快照，不持续监控。只核原成果的引用定位；不代表正文逐句准确，也不覆盖后续 DOCX/PDF 转换。
   </p>
  </section>
 )
}
