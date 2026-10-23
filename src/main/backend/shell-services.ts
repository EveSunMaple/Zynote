import {type BrowserWindow, dialog, safeStorage} from 'electron'
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

export function createShellServices(window: BrowserWindow): Record<string, ShellService> {
 return {
  'crypto.unprotect': params => {
   const value = params.value
   if (typeof value !== 'string' || !/^[A-Za-z0-9+/]+=*$/.test(value)) throw new Error('密文格式无效')
   assertSecureStorage()
   return {text: safeStorage.decryptString(Buffer.from(value, 'base64'))}
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
