import {readFile, writeFile, lstat, realpath} from 'node:fs/promises'
import {resolve, join} from 'node:path'
import type {NativeMediaSource} from '@main/media/core/media-source'
/** Writes only known names with exact-byte recovery; never overwrites a user-edited sidecar. */
export async function mediaSidecars(
 directory: string,
 source: NativeMediaSource,
 tuning: Record<string, number | true>
) {
 const base = resolve(directory)
 if ((await lstat(base)).isSymbolicLink() || resolve(await realpath(base)).toLowerCase() !== base.toLowerCase())
  throw new Error('附属文件目录无效')
 const outputs: {relative: string; text: string}[] = []
 if (tuning['--write-description'])
  outputs.push({relative: 'description.txt', text: (source.description || '此视频没有提供简介。') + '\n'})
 if (tuning['--write-link'])
  outputs.push({
   relative: 'source.webloc',
   text:
    '<?xml version="1.0" encoding="UTF-8"?>\n<plist version="1.0"><dict><key>URL</key><string>' +
    source.catalog.url.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;') +
    '</string></dict></plist>\n'
  })
 for (const output of outputs) {
  const path = join(base, output.relative)
  try {
   // biome-ignore format: NOSONAR must stay on the await line
   await writeFile(path, output.text, {flag: 'wx'}) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  } catch (error) {
   if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
   // biome-ignore format: NOSONAR must stay on the await line
   const item = await lstat(path) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
   if (!item.isFile() || item.isSymbolicLink()) throw new Error('附属文件路径无效')
  }
  // biome-ignore format: NOSONAR must stay on the await line
  if ((await readFile(path, 'utf8')) !== output.text) throw new Error('附属文件与当前来源不一致，保留原文件') // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
 }
 return outputs.map(output => output.relative)
}
