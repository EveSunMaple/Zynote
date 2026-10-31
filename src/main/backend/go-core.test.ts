import {mkdtempSync, mkdirSync, writeFileSync, rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {afterEach, expect, it, vi} from 'vitest'

const control = vi.hoisted(() => ({root: '', fixture: ''}))
vi.mock('electron', () => ({app: {isPackaged: false, getAppPath: () => control.root}}))
vi.mock('node:child_process', async importOriginal => {
 const actual = await importOriginal<typeof import('node:child_process')>()
 return {
  ...actual,
  spawn: (_binary: string, _args: unknown, options: object) =>
   actual.spawn(process.execPath, [control.fixture], options)
 }
})
import {GoCore} from './go-core'

let core: GoCore | undefined
afterEach(() => {
 core?.dispose()
 if (control.root) rmSync(control.root, {recursive: true, force: true})
})
it('retains stable codes from actual child JSON-RPC frames and sanitizes unknown wire errors', async () => {
 control.root = mkdtempSync(join(tmpdir(), 'zynote-wire-test-'))
 control.fixture = join(control.root, 'wire.cjs')
 mkdirSync(join(control.root, 'backend', 'bin'), {recursive: true})
 writeFileSync(
  join(control.root, 'backend', 'bin', process.platform === 'win32' ? 'zynote-core.exe' : 'zynote-core'),
  'fixture marker'
 )
 writeFileSync(
  control.fixture,
  `const readline=require('node:readline');readline.createInterface({input:process.stdin}).on('line',line=>{const request=JSON.parse(line);const response={jsonrpc:'2.0',id:request.id};if(request.method==='wire-error')response.error={code:request.params.code,message:'controlled wire message',data:{secret:'not exported'}};else response.result={nested:['original']};process.stdout.write(JSON.stringify(response)+'\\n')});`
 )
 const failures: string[] = []
 core = new GoCore({
  appVersion: 'fixture',
  dataRoot: control.root,
  configRoot: control.root,
  appRoot: control.root,
  libraryRoot: control.root,
  previousLibraryRoot: '',
  resourcesRoot: control.root,
  promptsRoot: control.root,
  nativePath: 'unused',
  portable: false,
  services: {},
  onEvent: () => {},
  onFailed: reason => failures.push(reason)
 })
 core.start()
 const deadline = Date.now() + 5000
 while (core.currentStatus !== 'ready') {
  if (Date.now() > deadline) throw new Error('fixture handshake timed out')
  await new Promise(resolve => setTimeout(resolve, 10))
 }
 expect(await core.call('success', {})).toEqual({nested: ['original']})
 for (const code of ['CANCELLED', 'TIMEOUT', 'AUTH_EXPIRED', 'RATE_LIMITED', 'BUSY']) {
  await expect(core.call('wire-error', {code})).rejects.toMatchObject({code, message: 'controlled wire message'})
 }
 try {
  await core.call('wire-error', {code: 'UNKNOWN_WIRE_CODE'})
  throw new Error('accepted failure')
 } catch (error) {
  expect(error).toMatchObject({code: 'REQUEST_FAILED', message: '操作失败，请稍后重试'})
  expect(error).not.toHaveProperty('data')
 }
 expect(failures).toEqual([])
})
