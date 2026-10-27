import {readFileSync, writeFileSync, renameSync, existsSync} from 'node:fs'
import {join} from 'node:path'
import {defaultIdentity, identitySchema, type Identity} from '@shared/identity'
export class IdentityStore {
 private readonly path: string
 constructor(root: string) {
  this.path = join(root, 'identity.json')
 }
 get(): Identity {
  if (!existsSync(this.path)) return {...defaultIdentity}
  const value = identitySchema.parse(JSON.parse(readFileSync(this.path, 'utf8')))
  return {
   ...value,
   persona: value.persona.trim() ? value.persona : defaultIdentity.persona,
   userName: value.userName === '你' ? defaultIdentity.userName : value.userName,
   assistantName: ['听课助手', '梅酱'].includes(value.assistantName)
    ? defaultIdentity.assistantName
    : value.assistantName
  }
 }
 save(value: unknown) {
  const parsed = identitySchema.parse(value)
  writeFileSync(this.path + '.tmp', JSON.stringify(parsed), 'utf8')
  renameSync(this.path + '.tmp', this.path)
  return parsed
 }
}
