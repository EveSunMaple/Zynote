import {type BrowserWindow, dialog, safeStorage, session, shell} from 'electron'
import {lstat} from 'node:fs/promises'
import {resolve, sep} from 'node:path'
import {z} from 'zod'
import {assertSecureStorage, systemProtect} from '@main/security/system-encryption'
import type {ShellService} from '@main/backend/shell-service'

function decodeBytes(value: unknown) {
 if (typeof value !== 'string' || !/^[A-Za-z0-9+/]*=*$/.test(value)) throw new Error('密文格式无效')
 return Buffer.from(value, 'base64')
}

function domainOf(value: unknown) {
 return value === 'im' ? 'im' : 'media'
}

export function createShellServices(window: BrowserWindow, libraryRoot: string): Record<string, ShellService> {
 return {
  'crypto.unprotect': params => {
   const value = params.value
   if (typeof value !== 'string' || !/^[A-Za-z0-9+/]+=*$/.test(value)) throw new Error('密文格式无效')
   assertSecureStorage()
   return {text: safeStorage.decryptString(Buffer.from(value, 'base64'))}
  },
  'crypto.protect': params => {
   const value = params.value
   if (typeof value !== 'string' || !value || value.length > 1000000) throw new Error('凭据格式无效')
   assertSecureStorage()
   return {value: safeStorage.encryptString(value).toString('base64')}
  },
  'crypto.protect-bytes': params => {
   assertSecureStorage()
   const raw = decodeBytes(params.value)
   return {value: systemProtect(raw, false, domainOf(params.domain)).toString('base64')}
  },
  'crypto.unprotect-bytes': params => {
   assertSecureStorage()
   const raw = decodeBytes(params.value)
   return {value: systemProtect(raw, true, domainOf(params.domain)).toString('base64')}
  },
  'shell.zhiyunSession': async () => {
   const cookies = await session.fromPartition('persist:zhiyun').cookies.get({
    url: 'https://onlineroom.cmc.zju.edu.cn'
   })
   const cookie = cookies.map(item => item.name + '=' + item.value).join('; ')
   const raw = cookies.find(item => item.name === '_token')?.value || ''
   let value = raw
   try {
    value = decodeURIComponent(raw)
   } catch {}
   const token = /{i:\d+;s:\d+:"_token";i:\d+;s:\d+:"(.+?)";}/.exec(value)?.[1]
   return {cookie, authorization: token ? 'Bearer ' + token : ''}
  },
  'shell.trashItem': async params => {
   const path = z.string().min(1).max(4096).parse(params.path),
    target = resolve(path)
   if (!target.startsWith(resolve(libraryRoot) + sep)) throw new Error('只能删除资料文件夹中的文件')
   const info = await lstat(target)
   if (info.isSymbolicLink() || !info.isFile()) throw new Error('只能删除资料文件夹中的文件')
   await shell.trashItem(target)
   return true
  },
  'shell.openPath': async params => {
   const path = z.string().min(1).max(4096).parse(params.path)
   const error = await shell.openPath(path)
   if (error) throw new Error(error)
   return true
  },
  'shell.pickFile': async params => {
   const title = z.string().min(1).max(200).parse(params.title)
   const extensions = z
    .array(z.string().min(1).max(20))
    .max(20)
    .parse(params.extensions || [])
   const picked = await dialog.showOpenDialog(window, {
    title,
    properties: ['openFile'],
    ...(extensions.length ? {filters: [{name: '文件', extensions}]} : {})
   })
   return {path: picked.canceled ? null : (picked.filePaths[0] ?? null)}
  }
 }
}
