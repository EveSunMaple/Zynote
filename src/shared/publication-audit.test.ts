import {expect, it} from 'vitest'
import {
 publicationAuditKey,
 publicationAuditMessage,
 PublicationAuditSession,
 type PublicationAuditTarget,
 type PublicationAuditResult
} from './publication-audit'

const target: PublicationAuditTarget = {
 id: 'task',
 itemIndex: 0,
 record: {
  state: 'recorded',
  planSHA256: 'a'.repeat(64),
  receiptSHA256: 'b'.repeat(64),
  mdPositionScope: 'anchor-appendix'
 }
}
it('rejects late responses after every identity, ref, scope or source change', () => {
 const variants: PublicationAuditTarget[] = [
  {...target, id: 'other'},
  {...target, itemIndex: 1},
  {...target, record: undefined},
  {...target, record: {...target.record!, planSHA256: 'c'.repeat(64)}},
  {...target, record: {...target.record!, receiptSHA256: 'd'.repeat(64)}},
  {...target, record: {...target.record!, state: 'unsupported'}},
  {...target, record: {...target.record!, mdPositionScope: 'none'}}
 ]
 for (const next of variants) {
  const session = new PublicationAuditSession(),
   first = publicationAuditKey(target, 'source')
  const token = session.start(first)!
  expect(session.start(first)).toBeNull()
  const changed = publicationAuditKey(next, 'source')
  expect(changed).not.toBe(first)
  session.change(changed)
  const current = session.start(changed)!
  expect(session.finish(token)).toBe(false)
  expect(session.finish(current)).toBe(true)
 }
 const session = new PublicationAuditSession(),
  token = session.start(publicationAuditKey(target, 'source'))!
 session.change(publicationAuditKey(target, 'changed source'))
 expect(session.finish(token)).toBe(false)
 const leaving = session.start('view')!
 session.invalidate()
 expect(session.finish(leaving)).toBe(false)
})
it('only describes completed checks with timestamps and limits Markdown scope', () => {
 const result: PublicationAuditResult = {
  state: 'complete',
  attemptedAt: 'start',
  checkedAt: 'checked',
  taskID: 'task',
  itemIndex: 0,
  checkedFiles: 2,
  verifiedPoints: 3,
  mdPositionScope: 'anchor-appendix',
  requestChainState: 'complete'
 }
 expect(publicationAuditMessage(result)).toContain('引用附录与 JSON')
 expect(publicationAuditMessage(result)).toContain('checked')
 expect(publicationAuditMessage({...result, mdPositionScope: 'none'})).toContain('无 Markdown 引用定位')
 for (const state of ['unknown', 'incomplete', 'invalid'] as const)
  expect(publicationAuditMessage({...result, state})).not.toContain('核验通过')
 expect(publicationAuditMessage({...result, checkedAt: undefined})).not.toContain('核验通过')
 expect(publicationAuditMessage({...result, state: 'unknown', reason: 'deadline', checkedAt: undefined})).toContain(
  '未完成'
 )
})
