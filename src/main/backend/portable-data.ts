import {existsSync, renameSync} from 'node:fs'
import {access, cp, mkdir, readFile, writeFile} from 'node:fs/promises'
import {join} from 'node:path'

const categories = ['PPT', 'Transcripts', 'Notes', 'PastExams']

async function exists(path: string) {
 try {
  await access(path)
  return true
 } catch {
  return false
 }
}

function renameLayout(root: string) {
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
}

async function copyLegacyDatabase(source: string, target: string) {
 if (!(await exists(source))) return
 for (const suffix of ['', '-wal', '-shm']) {
  const from = source + suffix
  if (await exists(from)) await cp(from, target + suffix, {force: false})
 }
}

async function migratePortableLayout(root: string, appDirectory: string) {
 const sourceData = join(appDirectory, 'Data'),
  targetDatabase = join(root, 'assistant.db'),
  sourceDatabase = join(sourceData, 'assistant.db')
 if ((await exists(targetDatabase)) || !(await exists(sourceDatabase))) return
 await mkdir(root, {recursive: true})
 await copyLegacyDatabase(sourceDatabase, targetDatabase)
 for (const name of ['library-location.json'] as const) {
  const source = join(sourceData, name),
   target = join(root, name)
  if ((await exists(source)) && !(await exists(target))) await cp(source, target, {force: false})
 }
 for (const name of ['Backups', 'Library'] as const) {
  const source = join(name === 'Library' ? appDirectory : sourceData, name),
   target = join(root, name)
  if ((await exists(source)) && !(await exists(target)))
   await cp(source, target, {recursive: true, force: false, errorOnExist: false})
 }
}

export async function resolveDataPaths({
 root,
 appDirectory,
 legacyLibrary,
 portable
}: {
 root: string
 appDirectory: string
 legacyLibrary: string
 portable: boolean
}) {
 if (!portable) {
  if (appDirectory !== root) await migratePortableLayout(root, appDirectory)
  const legacyLayout = join(root, '资料'),
   hadLegacyLayout = existsSync(legacyLayout)
  renameLayout(root)
  const libraryRoot = join(root, existsSync(join(root, 'Library')) ? 'Library' : '资料')
  const legacyMedia = join(libraryRoot, '媒体下载'),
   mediaDownloads = join(libraryRoot, 'MediaDownloads')
  if (existsSync(legacyMedia) && !existsSync(mediaDownloads)) {
   try {
    renameSync(legacyMedia, mediaDownloads)
   } catch {}
  }
  return {
   dataRoot: root,
   libraryRoot,
   previousLibraryRoot: hadLegacyLayout ? legacyLayout : '',
   portable: false
  }
 }

 const legacyLayout = join(appDirectory, '资料'),
  hadLegacyLayout = existsSync(legacyLayout) && !existsSync(join(appDirectory, 'Library'))
 renameLayout(appDirectory)
 const dataRoot = join(appDirectory, 'Data'),
  defaultLibrary = join(appDirectory, 'Library'),
  location = join(dataRoot, 'library-location.json'),
  database = join(dataRoot, 'assistant.db')
 await mkdir(dataRoot, {recursive: true})
 let libraryRoot = defaultLibrary,
  previousLibraryRoot = hadLegacyLayout ? legacyLayout : ''
 if (await exists(location)) {
  try {
   const saved = JSON.parse(await readFile(location, 'utf8')) as {libraryRoot?: unknown}
   if (typeof saved.libraryRoot === 'string' && saved.libraryRoot) {
    libraryRoot = saved.libraryRoot
    previousLibraryRoot = saved.libraryRoot
   }
  } catch {}
 }
 if (!(await exists(database))) {
  if (!previousLibraryRoot) previousLibraryRoot = legacyLibrary
  for (const category of categories) {
   const source = join(legacyLibrary, category)
   if (await exists(source)) await cp(source, join(defaultLibrary, category), {recursive: true, force: false})
  }
  for (const source of [
   join(root, 'Data', 'assistant.db'),
   join(root, '课程数据', 'assistant.db'),
   join(root, 'assistant.db')
  ]) {
   if (await exists(source)) {
    await copyLegacyDatabase(source, database)
    break
   }
  }
 }
 await mkdir(libraryRoot, {recursive: true})
 await writeFile(location, JSON.stringify({libraryRoot}), 'utf8')
 return {dataRoot, libraryRoot, previousLibraryRoot, portable: true}
}
