export type VisualOutputRecovery = 'journal' | 'pending' | 'changed' | 'legacy'

export type VisualCoverageLevel = {
 state: 'recorded' | 'partial' | 'unknown' | 'invalid'
 recordedCount: number
 total: number
 reason?: string
}
export type VisualCoverage = {
 extracted: VisualCoverageLevel
 recognized: VisualCoverageLevel
 submitted: VisualCoverageLevel
 cited: VisualCoverageLevel
}
export function visualCoverageRows(coverage?: VisualCoverage) {
 return (
  [
   ['extracted', '已提取'],
   ['recognized', '已识别并保存文字'],
   ['submitted', '已送入摘要'],
   ['cited', '已被摘要引用']
  ] as const
 ).map(([key, title]) => {
  const level = coverage?.[key]
  let value = '未记录'
  if (level?.state === 'invalid') {
   value =
    level.reason === 'output-recovery-unverified' ? '记录失效；恢复文件已生成，历史覆盖记录需重新核对' : '记录失效'
  } else if (level?.state === 'recorded' || level?.state === 'partial') {
   value =
    Number.isSafeInteger(level.recordedCount) &&
    Number.isSafeInteger(level.total) &&
    level.recordedCount >= 0 &&
    level.recordedCount <= level.total
     ? `${level.recordedCount}/${level.total} 个所选画面`
     : '记录失效'
  } else if (level?.reason === 'not-integrated') value = '未记录（尚未接入）'
  return {key, title, value}
 })
}

export interface VisualOutputState {
 currentHash?: string
 hasOutput?: boolean
 hasLegacyOutput?: boolean
}

/**
 * Classifies a saved visual task without changing any files.  The caller supplies
 * the digest of the canonical output (when it exists) so this helper stays pure
 * and can also be used by the renderer's tests.
 */
export function visualOutputRecoveryReason(
 task: {
  output?: string
  outputHash?: string
  outputJournal?: unknown
  outputPending?: boolean
  cleaned?: boolean
  frames: Array<{status: string}>
 },
 state: VisualOutputState = {}
): VisualOutputRecovery | undefined {
 if (task.outputJournal) return 'journal'
 if (task.outputPending) return 'pending'
 if (task.output && (!task.outputHash || state.hasOutput !== true || state.currentHash !== task.outputHash))
  return 'changed'
 if (
  (state.hasLegacyOutput || !task.output || task.cleaned !== true) &&
  task.frames.length > 0 &&
  task.frames.every(frame => frame.status === '已完成')
 )
  return 'legacy'
 return undefined
}
