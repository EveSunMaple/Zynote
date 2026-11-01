import type {ChildProcess} from 'node:child_process'
import {mkdtemp, mkdir, writeFile, readFile, rm, realpath} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createHash} from 'node:crypto'
import {setTimeout as realDelay} from 'node:timers/promises'
import {afterEach, expect, it, vi} from 'vitest'
import {GoCore} from '@main/backend/go-core'
import {ManagedPlayback} from '@main/media/managed-playback'
import {mediaResponse} from '@main/media/media-protocol'

const control = vi.hoisted(() => ({child: undefined as ChildProcess | undefined}))
vi.mock('electron', () => ({app: {isPackaged: false, getAppPath: () => '/controlled-fixture'}}))
vi.mock('node:fs', async original => ({...(await original<typeof import('node:fs')>()), existsSync: () => true}))
vi.mock('node:child_process', async original => {
 const actual = await original<typeof import('node:child_process')>()
 return {
  ...actual,
  spawn: () => {
   control.child = actual.spawn(
    process.execPath,
    [
     '-e',
     `
const readline=require('node:readline'),fs=require('node:fs'),path=require('node:path');
let root='',holdPrepare=false,plan;const pending=[];
const send=frame=>process.stdout.write(JSON.stringify({jsonrpc:'2.0',...frame})+'\\n');
const event=action=>send({method:'event',params:{name:'received',action}});
const reply=(frame,result)=>send({id:frame.id,result});
const prepared=()=>({plan:{...plan,state:'prepared'},tombstoneSHA256:'c'.repeat(64)});
readline.createInterface({input:process.stdin}).on('line',line=>{
 const frame=JSON.parse(line),p=frame.params||{};
 if(frame.method==='core.hello'){root=p.libraryRoot;reply(frame,{})}
 else if(frame.method==='fixture.hold-prepare'){holdPrepare=true;event(frame.method)}
 else if(frame.method==='fixture.reply'){
  holdPrepare=false;
  for(const request of pending.splice(0))reply(request,request.method.includes('prepare-')?prepared():p.declaration);
  event(frame.method);
 }else if(frame.method==='video-ppt:prepare-playback-release'){
  event(frame.method);plan ||= {version:1,id:p.id,refKind:p.expectedRefKind,refSHA256:p.expectedRefSHA256,releaseID:p.releaseID,files:[]};
  if(holdPrepare)pending.push(frame);else reply(frame,prepared());
 }else if(frame.method==='video-ppt:finalize-playback-release'){
  event(frame.method);fs.unlinkSync(path.join(root,'VideoTasks',p.id,'video.mp4'));
  reply(frame,{plan:{...plan,state:'released'},tombstoneSHA256:'c'.repeat(64)});
 }else{pending.push(frame);event(frame.method)}
});
`
    ],
    {stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true}
   )
   return control.child
  }
 }
})

const taskID = '9e326025-30b4-45d0-bd27-4a30275adccb'
const requestID = '9e326025-30b4-45d0-bd27-4a30275adcc1'
const releaseInput = {videoTaskID: taskID, expectedRefKind: 'intent', expectedRefSHA256: 'b'.repeat(64)}
let core: GoCore | undefined,
 manager: ManagedPlayback | undefined,
 root = ''
async function until(predicate: () => boolean) {
 for (let i = 0; i < 500; i++) {
  if (predicate()) return
  await realDelay(10)
 }
 throw new Error('fixture did not reach boundary')
}
afterEach(async () => {
 vi.useRealTimers()
 const child = control.child
 const exited =
  child && child.exitCode === null && child.signalCode === null
   ? new Promise<void>(done => child.once('exit', () => done()))
   : Promise.resolve()
 core?.dispose()
 child?.kill()
 await exited
 await manager?.dispose()
 if (root) await rm(root, {recursive: true, force: true, maxRetries: 5, retryDelay: 20})
 core = undefined
 manager = undefined
 control.child = undefined
 root = ''
})
async function fixture() {
 root = await realpath(await mkdtemp(join(tmpdir(), 'zynote-physical-rpc-')))
 const path = join(root, 'VideoTasks', taskID, 'video.mp4'),
  bytes = Buffer.from('managed fixture media')
 await mkdir(join(root, 'VideoTasks', taskID), {recursive: true})
 await writeFile(path, bytes)
 const events: string[] = [],
  sha = createHash('sha256').update(bytes).digest('hex')
 const declaration = {
  version: 1,
  verification: 'declared',
  taskID,
  sourceKind: 'local',
  sourceIdentity: 'sha256:' + sha,
  sourceSHA256: sha,
  size: bytes.length,
  bindingRevision: 'a'.repeat(64)
 }
 core = new GoCore({
  appVersion: 'fixture',
  dataRoot: root,
  configRoot: root,
  appRoot: root,
  libraryRoot: root,
  previousLibraryRoot: '',
  resourcesRoot: root,
  promptsRoot: root,
  nativePath: '',
  portable: false,
  services: {},
  onEvent: event => events.push((event as unknown as {action: string}).action),
  onFailed: () => {}
 })
 core.start()
 await until(() => core?.currentStatus === 'ready')
 manager = new ManagedPlayback(
  root,
  async () => {
   throw new Error('untracked declaration forbidden')
  },
  undefined,
  (action, input) => core!.callTracked(action, input)
 )
 return {
  path,
  bytes,
  events,
  sha,
  declaration,
  reply: (value: unknown = declaration) =>
   control.child!.stdin!.write(JSON.stringify({method: 'fixture.reply', params: {declaration: value}}) + '\n')
 }
}

it('retains a bound HEAD slot and file through actual check RPC timeout until late wire permits release', async () => {
 const f = await fixture()
 const declaration = {...f.declaration, sourceBindingSHA256: 'b'.repeat(64), bindingAuthoritySHA256: 'e'.repeat(64)}
 const opening = manager!.openBound({
  requestID,
  summaryTaskID: '9e326025-30b4-45d0-bd27-4a30275adcca',
  itemIndex: 0,
  expectedSourceBindingSHA256: 'b'.repeat(64)
 })
 void opening.catch(() => {})
 await until(() => f.events.filter(action => action === 'video-ppt:bound-playback-source').length === 1)
 f.reply(declaration)
 await until(() => f.events.filter(action => action === 'video-ppt:bound-playback-source').length === 2)
 f.reply(declaration)
 const opened = await opening,
  token = manager!.get(opened.tokenURL.split('/').at(-1)!)!
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const head = mediaResponse(manager!, new Request(opened.tokenURL, {method: 'HEAD'}))
 await until(() => f.events.includes('video-ppt:bound-playback-check'))
 await vi.advanceTimersByTimeAsync(6000)
 expect((await head).status).toBe(404)
 expect(token.streams.size).toBe(1)
 expect(token.reads.size).toBe(1)
 const release = manager!.release(releaseInput)
 await until(() => f.events.includes('video-ppt:prepare-playback-release'))
 expect(f.events).not.toContain('video-ppt:finalize-playback-release')
 expect(await readFile(f.path)).toEqual(f.bytes)
 f.reply(declaration)
 await expect(release).resolves.toMatchObject({state: 'released'})
 expect(token.streams.size).toBe(0)
 expect(token.reads.size).toBe(0)
}, 15000)

it('drains unknown-task real RPC after its logical6s timeout before release can finalize', async () => {
 const f = await fixture()
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const observed = manager!
  .observeRPC(undefined, core!.callTracked('video-ppt:bound-playback-source', {}))
  .catch(error => error)
 await until(() => f.events.includes('video-ppt:bound-playback-source'))
 await vi.advanceTimersByTimeAsync(6000)
 expect(await observed).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
 const releasing = manager!.release(releaseInput)
 await until(() => f.events.includes('video-ppt:prepare-playback-release'))
 expect(f.events).not.toContain('video-ppt:finalize-playback-release')
 expect(await readFile(f.path)).toEqual(f.bytes)
 f.reply()
 await expect(releasing).resolves.toMatchObject({state: 'released'})
 await expect(readFile(f.path)).rejects.toMatchObject({code: 'ENOENT'})
}, 15000)

it('holds known-task mint reservation after real logical timeout and rejects late mint on release', async () => {
 const f = await fixture()
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const opening = manager!.open({requestID, videoTaskID: taskID, expectedSourceSHA256: f.sha}).catch(error => error)
 await until(() => f.events.includes('video-ppt:playback-source'))
 await vi.advanceTimersByTimeAsync(90000)
 expect(await opening).toMatchObject({code: 'TIMEOUT'})
 await expect(
  manager!.open({requestID: '9e326025-30b4-45d0-bd27-4a30275adcc2', videoTaskID: taskID, expectedSourceSHA256: f.sha})
 ).rejects.toMatchObject({code: 'BUSY'})
 await vi.advanceTimersByTimeAsync(30000)
 const releasing = manager!.release(releaseInput)
 await until(() => f.events.includes('video-ppt:prepare-playback-release'))
 expect(f.events).not.toContain('video-ppt:finalize-playback-release')
 f.reply()
 await expect(releasing).resolves.toMatchObject({state: 'released'})
 expect(f.events.filter(action => action === 'video-ppt:playback-source')).toHaveLength(1)
}, 15000)

it('actual child exit ends physical metadata but cannot authorize release finalize on an unavailable backend', async () => {
 const f = await fixture()
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const observed = manager!
  .observeRPC(undefined, core!.callTracked('video-ppt:bound-playback-source', {}))
  .catch(error => error)
 await until(() => f.events.includes('video-ppt:bound-playback-source'))
 await vi.advanceTimersByTimeAsync(6000)
 await observed
 const releasing = manager!.release(releaseInput).catch(error => error)
 await until(() => f.events.includes('video-ppt:prepare-playback-release'))
 const child = control.child!,
  exited = new Promise<void>(done => child.once('exit', () => done()))
 child.kill()
 await exited
 expect(await releasing).toMatchObject({message: 'Go 后端不可用'})
 expect(f.events).not.toContain('video-ppt:finalize-playback-release')
 expect(await readFile(f.path)).toEqual(f.bytes)
}, 15000)

it('retains failed-close quota with tracked real declarations and retries close before finalize', async () => {
 const f = await fixture()
 const opening = manager!.open({requestID, videoTaskID: taskID, expectedSourceSHA256: f.sha})
 void opening.catch(() => {})
 await until(() => f.events.filter(action => action === 'video-ppt:playback-source').length === 1)
 f.reply()
 await until(() => f.events.filter(action => action === 'video-ppt:playback-source').length === 2)
 f.reply()
 const opened = await opening,
  token = manager!.get(opened.tokenURL.split('/').at(-1)!)!
 const close = vi.spyOn(token.file, 'close').mockRejectedValue(new Error('controlled close failure'))
 await expect(manager!.release(releaseInput)).rejects.toThrow('controlled close failure')
 expect(f.events).not.toContain('video-ppt:finalize-playback-release')
 expect(await readFile(f.path)).toEqual(f.bytes)
 close.mockRestore()
 await expect(manager!.release(releaseInput)).resolves.toMatchObject({state: 'released'})
}, 15000)

it('keeps the release in-flight fence after logical timeout until actual wire terminal', async () => {
 const f = await fixture()
 control.child!.stdin!.write(JSON.stringify({method: 'fixture.hold-prepare'}) + '\n')
 await until(() => f.events.includes('fixture.hold-prepare'))
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const releasing = manager!.release(releaseInput).catch(error => error)
 await until(() => f.events.includes('video-ppt:prepare-playback-release'))
 await vi.advanceTimersByTimeAsync(30 * 60 * 1000)
 expect(await releasing).toMatchObject({code: 'TIMEOUT', message: 'Go 后端请求超时'})
 for (let i = 0; i < 4; i++) await expect(manager!.release(releaseInput)).rejects.toMatchObject({code: 'BUSY'})
 expect(f.events.filter(action => action === 'video-ppt:prepare-playback-release')).toHaveLength(1)
 expect(await readFile(f.path)).toEqual(f.bytes)
 f.reply()
 await until(() => f.events.includes('fixture.reply'))
 await expect(manager!.release(releaseInput)).resolves.toMatchObject({state: 'released'})
 expect(f.events.filter(action => action === 'video-ppt:prepare-playback-release')).toHaveLength(2)
}, 15000)

it('cancels a real pending mint on release and never mints its late successful declaration', async () => {
 const f = await fixture()
 const opening = manager!.open({requestID, videoTaskID: taskID, expectedSourceSHA256: f.sha}).catch(error => error)
 await until(() => f.events.includes('video-ppt:playback-source'))
 const releasing = manager!.release(releaseInput)
 await until(() => f.events.includes('video-ppt:prepare-playback-release'))
 expect(await opening).toMatchObject({code: 'CANCELLED'})
 expect(f.events).not.toContain('video-ppt:finalize-playback-release')
 f.reply()
 await expect(releasing).resolves.toMatchObject({state: 'released'})
 expect(f.events.filter(action => action === 'video-ppt:playback-source')).toHaveLength(1)
}, 15000)
