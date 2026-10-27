import {mkdir, open} from 'node:fs/promises'
import {join, basename, extname} from 'node:path'
import {stripTrailing} from '@shared/text-trim'

export const categories = ['PPT', 'Transcripts', 'Notes', 'PastExams'] as const
export type Category = (typeof categories)[number]
export function folderName(name: string) {
 const value =
  stripTrailing(stripTrailing(name.replace(/[<>:"/\\|?*\x00-\x1f]/g, '_'), '. ').slice(0, 100), '. ') || '未命名课程'
 return /^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)/i.test(value) ? '_' + value : value
}
export async function libraryDirectory(root: string, category: Category, course?: string) {
 const path = course ? join(root, category, folderName(course)) : join(root, category)
 await mkdir(path, {recursive: true})
 return path
}
export async function ensureLibrary(root: string, courses: string[] = []) {
 for (const category of categories) {
  await libraryDirectory(root, category)
  for (const course of courses) await libraryDirectory(root, category, course)
 }
}
export async function reserveFile(directory: string, filename: string) {
 await mkdir(directory, {recursive: true})
 const ext = extname(filename),
  stem = folderName(basename(filename, ext))
 for (let i = 1; ; i++) {
  const path = join(directory, stem + (i === 1 ? '' : ` (${i})`) + ext)
  try {
   const file = await open(path, 'wx')
   await file.close()
   return path
  } catch (error) {
   if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
  }
 }
}
