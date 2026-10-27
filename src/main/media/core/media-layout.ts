import {copyFile, lstat, mkdir, realpath} from 'node:fs/promises'
import {constants} from 'node:fs'
import {join, resolve} from 'node:path'
import type {NativeMediaOptions} from '@main/media/core/media-options'
import {mediaFileHash} from '@main/media/core/media-artifact'
/** Layout copies are verified before original staging files become eligible for cleanup. */
export async function publishMediaLayout(
 directory: string,
 names: string[],
 options: NativeMediaOptions,
 created: string
) {
 if (options.folder_layout === 'source') return {outputs: names, cleanup: [] as string[]}
 const base = resolve(directory)
 if ((await lstat(base)).isSymbolicLink() || resolve(await realpath(base)).toLowerCase() !== base.toLowerCase())
  throw new Error('目录整理根路径无效')
 const parts: string[] = []
 if (options.folder_layout.includes('type')) {
  let mediaType = '视频'
  if (options.subtitle_only) mediaType = '字幕'
  else if (options.audio_only) mediaType = '音频'
  parts.push(mediaType)
 }
 if (options.folder_layout.includes('date')) {
  const date = /^\d{4}-(?:0[1-9]|1[0-2])/.exec(created)?.[0]
  if (!date) throw new Error('下载创建月份无效')
  parts.push(date)
 }
 let folder = base
 for (const part of parts) {
  folder = join(folder, part)
  // biome-ignore format: NOSONAR must stay on the await line
  await mkdir(folder, {recursive: true}) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  // biome-ignore format: NOSONAR must stay on the await line
  if ((await lstat(folder)).isSymbolicLink() || resolve(await realpath(folder)).toLowerCase() !== folder.toLowerCase()) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
   throw new Error('整理目录包含链接')
 }
 for (const name of names) {
  if (
   !name ||
   /[\\/:\x00-\x1f]/.test(name) ||
   name.startsWith('.') ||
   /[. ]$/.test(name) ||
   ['job.json', 'job.writing'].includes(name.toLowerCase())
  )
   throw new Error('待整理成果文件名无效')
  const source = join(base, name),
   target = join(folder, name),
   // biome-ignore format: NOSONAR must stay on the await line
   item = await lstat(source) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  if (!item.isFile() || item.isSymbolicLink()) throw new Error('待整理成果不是普通文件')
  // biome-ignore format: NOSONAR must stay on the await line
  const hash = await mediaFileHash(source) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  try {
   // biome-ignore format: NOSONAR must stay on the await line
   await copyFile(source, target, constants.COPYFILE_EXCL) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  } catch (error) {
   if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
  }
  // biome-ignore format: NOSONAR must stay on the await line
  const copied = await lstat(target) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  if (
   !copied.isFile() ||
   copied.isSymbolicLink() ||
   copied.size !== item.size ||
   // biome-ignore format: NOSONAR must stay on the await line
   (await mediaFileHash(target)) !== hash || // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
   (await mediaFileHash(source)) !== hash
  )
   throw new Error('目录整理成果校验失败，原文件和已有目标均保留')
 }
 return {outputs: names.map(name => [...parts, name].join('/')), cleanup: [...names]}
}
