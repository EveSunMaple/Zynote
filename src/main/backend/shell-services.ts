import {safeStorage} from 'electron'
import {assertSecureStorage} from '@main/security/system-encryption'
import type {ShellService} from '@main/backend/shell-service'

export const shellServices: Record<string, ShellService> = {
 'crypto.unprotect': params => {
  const value = params.value
  if (typeof value !== 'string' || !/^[A-Za-z0-9+/]+=*$/.test(value)) throw new Error('密文格式无效')
  assertSecureStorage()
  return {text: safeStorage.decryptString(Buffer.from(value, 'base64'))}
 }
}
