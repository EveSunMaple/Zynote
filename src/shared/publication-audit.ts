export type PublicationRecord = {
 state: 'recorded' | 'pending' | 'unsupported' | 'invalid'
 planSHA256?: string
 receiptSHA256?: string
 pointCount?: number
 mdPositionScope: 'anchor-appendix' | 'none'
}
export type PublicationAuditTarget = {id: string; itemIndex: number; record?: PublicationRecord | null}
export type PublicationAuditResult = {
 state: 'unknown' | 'incomplete' | 'invalid' | 'complete'
 reason?: string
 attemptedAt: string
 checkedAt?: string
 taskID: string
 itemIndex: number
 planSHA256?: string
 receiptSHA256?: string
 sourceSHA256?: string
 checkedFiles: number
 verifiedPoints: number
 mdPositionScope: 'anchor-appendix' | 'none'
 jsonPositionScope?: 'point-array'
 requestChainState: string
}
export const publicationAuditKey = (target: PublicationAuditTarget, sourceHash?: string) =>
 JSON.stringify([
  target.id,
  target.itemIndex,
  target.record?.planSHA256 || '',
  target.record?.receiptSHA256 || '',
  target.record?.state || 'absent',
  target.record?.mdPositionScope || 'unknown',
  sourceHash || ''
 ])

// An in-flight response belongs to its original item and immutable refs.
export class PublicationAuditSession {
 private key = ''
 private generation = 0
 private pending = false
 change(key: string) {
  if (this.key !== key) {
   this.key = key
   this.generation++
   this.pending = false
  }
 }
 start(key: string) {
  this.change(key)
  if (this.pending) return null
  this.pending = true
  return {key, generation: ++this.generation}
 }
 finish(token: {key: string; generation: number}) {
  if (this.key !== token.key || this.generation !== token.generation) return false
  this.pending = false
  return true
 }
 invalidate() {
  this.generation++
  this.pending = false
 }
}
export function publicationAuditMessage(result: PublicationAuditResult) {
 if (result.state === 'complete' && result.checkedAt)
  return result.mdPositionScope === 'anchor-appendix'
   ? `上次核验通过：引用附录与 JSON 要点匹配（${result.verifiedPoints} 项，${result.checkedAt}）。`
   : `上次核验通过：JSON 要点匹配（${result.verifiedPoints} 项，${result.checkedAt}）；此格式无 Markdown 引用定位。`
 if (result.state === 'invalid') return '本次核验未通过：保存凭据、来源或成果已经不匹配；原文件保留。'
 if (result.state === 'incomplete') return '成果发布或请求凭据尚未完成；本次只读核验没有继续发布。'
 if (result.reason === 'deadline') return '本次核验未完成：达到时间预算，可再次核验。'
 if (result.reason === 'stale-record') return '保存记录已经变化，请刷新后重新核验。'
 if (result.reason === 'source-unavailable') return '当前无法读取来源字幕，尚不能核验成果引用。'
 return '本次尚不能核验成果引用；旧记录或不支持的凭据不会补造。'
}
