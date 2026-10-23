import {existsSync, renameSync} from 'node:fs'
import {join, relative, isAbsolute, resolve} from 'node:path'
import type {Store} from '@main/storage/db'

export function migrateDataLayout(root: string): {library: string} {
 for (const [from, to] of [
  ['课程数据', 'Data'],
  ['资料', 'Library'],
  ['诊断日志', 'Logs']
 ] as const) {
  const source = join(root, from),
   target = join(root, to)
  if (!existsSync(source) || existsSync(target)) continue
  try {
   renameSync(source, target)
  } catch {}
 }
 return {library: existsSync(join(root, 'Library')) ? 'Library' : existsSync(join(root, '资料')) ? '资料' : 'Library'}
}

export function migrateLibraryLayout(libraryRoot: string) {
 for (const [from, to] of [['媒体下载', 'MediaDownloads']] as const) {
  const source = join(libraryRoot, from),
   target = join(libraryRoot, to)
  if (!existsSync(source) || existsSync(target)) continue
  try {
   renameSync(source, target)
  } catch {}
 }
}

export function relocateStorePaths(store: Store, previousRoot: string, libraryRoot: string) {
 const from = resolve(previousRoot),
  to = resolve(libraryRoot)
 const relocate = (path: string) => {
  if (!path) return path
  const rel = relative(from, resolve(path))
  if (!rel || rel === '..' || rel.startsWith('..\\') || rel.startsWith('../') || isAbsolute(rel)) return path
  const target = join(to, rel)
  return existsSync(target) ? target : path
 }
 const updates: Array<() => void> = []
 for (const row of store.db.prepare('SELECT id,path FROM course_files').all() as {id: string; path: string}[]) {
  const path = relocate(row.path)
  if (path !== row.path)
   updates.push(() => {
    store.db.prepare('UPDATE course_files SET path=? WHERE id=?').run(path, row.id)
   })
 }
 for (const row of store.db.prepare('SELECT id,ppt_path,transcript_path,source_video_path FROM lectures').all() as {
  id: string
  ppt_path: string
  transcript_path: string
  source_video_path: string
 }[]) {
  const ppt = relocate(row.ppt_path),
   transcript = relocate(row.transcript_path),
   source = relocate(row.source_video_path)
  if (ppt !== row.ppt_path || transcript !== row.transcript_path || source !== row.source_video_path)
   updates.push(() => {
    store.db
     .prepare('UPDATE lectures SET ppt_path=?,transcript_path=?,source_video_path=? WHERE id=?')
     .run(ppt, transcript, source, row.id)
   })
 }
 if (!updates.length) return
 store.db.transaction(() => {
  for (const update of updates) update()
 })()
 store.db.pragma('wal_checkpoint(FULL)')
}
