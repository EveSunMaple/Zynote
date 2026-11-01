import {describe, expect, it} from 'vitest'
import {visualCoverageRows, type VisualCoverage} from './visual'

describe('visual factual coverage', () => {
 it('keeps legacy coverage unknown and distinguishes saved response counts from summary integration', () => {
  expect(visualCoverageRows().every(row => row.value === '未记录')).toBe(true)
  const coverage: VisualCoverage = {
   extracted: {state: 'recorded', recordedCount: 4, total: 4},
   recognized: {state: 'partial', recordedCount: 1, total: 4},
   submitted: {state: 'unknown', recordedCount: 0, total: 4, reason: 'not-integrated'},
   cited: {state: 'unknown', recordedCount: 0, total: 4, reason: 'not-integrated'}
  }
  expect(visualCoverageRows(coverage).map(row => row.value)).toEqual([
   '4/4 个所选画面',
   '1/4 个所选画面',
   '未记录（尚未接入）',
   '未记录（尚未接入）'
  ])
  coverage.recognized = {state: 'invalid', recordedCount: 4, total: 4}
  expect(visualCoverageRows(coverage)[1].value).toBe('记录失效')
  coverage.recognized.reason = 'output-recovery-unverified'
  expect(visualCoverageRows(coverage)[1].value).toContain('恢复文件已生成，历史覆盖记录需重新核对')
  coverage.extracted.recordedCount = 5
  expect(visualCoverageRows(coverage)[0].value).toBe('记录失效')
 })
})
