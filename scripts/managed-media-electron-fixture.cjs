const {app, BrowserWindow, protocol} = require('electron')
app.on('window-all-closed', () => {})
const {join} = require('node:path')
const fs = require('node:fs/promises')
const {createHash} = require('node:crypto')
const {ManagedPlayback} = require('./bundle/managed-playback.js')
const {installMediaProtocol} = require('./bundle/media-protocol.js')
app.setPath('userData', join(__dirname, 'isolated-profile'))
app.commandLine.appendSwitch('autoplay-policy', 'no-user-gesture-required')
protocol.registerSchemesAsPrivileged([
 {
  scheme: 'lecture-media',
  privileges: {standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true}
 }
])
const taskID = '9e326025-30b4-45d0-bd27-4a30275adccb',
 requestID = '9e326025-30b4-45d0-bd27-4a30275adcc1'
const report = {electron: process.versions.electron, platform: process.platform, range: [], media: [], errors: []}
let window, remove, manager
function check(value, message) {
 if (!value) throw new Error(message)
}
app.whenReady().then(async () => {
 try {
  const library = join(__dirname, 'isolated-library')
  await fs.mkdir(join(library, 'VideoTasks', taskID), {recursive: true})
  const bytes = await fs.readFile(join(__dirname, 'fixture.bin'))
  await fs.writeFile(join(library, 'VideoTasks', taskID, 'video.mp4'), bytes)
  const sha = createHash('sha256').update(bytes).digest('hex')
  report.fixture = {
   size: bytes.length,
   sha256: sha,
   codec: process.env.ZYNOTE_MEDIA_SMOKE_CODEC || 'caller-supplied fixture'
  }
  await fs.writeFile(
   join(__dirname, 'page.html'),
   '<!doctype html><meta charset="utf-8"><title>Isolated production media protocol test</title>'
  )
  const create = async webSecurity => {
   window = new BrowserWindow({
    show: false,
    webPreferences: {sandbox: true, nodeIntegration: false, contextIsolation: true, webSecurity}
   })
   manager = new ManagedPlayback(library, async () => ({
    version: 1,
    verification: 'declared',
    taskID,
    sourceKind: 'local',
    sourceIdentity: 'sha256:' + sha,
    sourceSHA256: sha,
    size: bytes.length,
    bindingRevision: 'a'.repeat(64)
   }))
   remove = installMediaProtocol(window.webContents.session, window, manager, undefined)
   await window.loadFile(join(__dirname, 'page.html'))
   return manager.open({requestID, videoTaskID: taskID, expectedSourceSHA256: sha})
  }
  // Only the HTTP byte-matrix window disables browser CORS, so actual Response bytes
  // can be inspected without adding ACAO to the production protocol. Playback below
  // uses normal webSecurity:true, matching the product's sandbox/security settings.
  const first = await create(false)
  for (const item of [
   {range: null, status: 200, start: 0, end: bytes.length - 1},
   {range: 'bytes=2-18', status: 206, start: 2, end: 18},
   {range: 'bytes=8-', status: 206, start: 8, end: bytes.length - 1},
   {range: 'bytes=-11', status: 206, start: bytes.length - 11, end: bytes.length - 1},
   {range: 'bytes=0-9999999', status: 206, start: 0, end: bytes.length - 1},
   {range: 'bytes=9999999-', status: 416},
   {range: 'bytes=2-1', status: 416},
   {range: 'bytes=0-1,4-5', status: 416},
   {range: 'bytes=-0', status: 416}
  ]) {
   const value = await window.webContents.executeJavaScript(
    `fetch(${JSON.stringify(first.tokenURL)},{headers:${JSON.stringify(item.range ? {Range: item.range} : {})}}).then(async r=>({status:r.status,headers:Object.fromEntries(r.headers),bytes:Array.from(new Uint8Array(await r.arrayBuffer()))}))`
   )
   check(value.status === item.status, 'range status ' + item.range + ': ' + value.status)
   if (item.start !== undefined)
    check(Buffer.from(value.bytes).equals(bytes.subarray(item.start, item.end + 1)), 'range bytes ' + item.range)
   check(!value.headers['access-control-allow-origin'], 'production must not add ACAO')
   report.range.push({
    range: item.range,
    status: value.status,
    length: value.bytes.length,
    contentRange: value.headers['content-range'] || null
   })
  }
  const deniedWindow = new BrowserWindow({
   show: false,
   webPreferences: {sandbox: true, nodeIntegration: false, contextIsolation: true, webSecurity: false}
  })
  await deniedWindow.loadFile(join(__dirname, 'page.html'))
  report.deniedWindow = await deniedWindow.webContents.executeJavaScript(
   `fetch(${JSON.stringify(first.tokenURL)}).then(()=>false,()=>true)`
  )
  check(report.deniedWindow, 'foreign window read was not blocked')
  deniedWindow.destroy()
  report.deniedFrame = await window.webContents.executeJavaScript(
   `new Promise(resolve=>{const f=document.createElement('iframe');f.srcdoc='<script>fetch(${JSON.stringify(first.tokenURL)}).then(()=>parent.postMessage(false,"*"),()=>parent.postMessage(true,"*"));<\\/script>';window.addEventListener('message',e=>resolve(e.data),{once:true});document.body.append(f);setTimeout(()=>resolve(false),3000)})`
  )
  check(report.deniedFrame, 'subframe read was not blocked')
  const head = await window.webContents.executeJavaScript(
   `fetch(${JSON.stringify(first.tokenURL)},{method:'HEAD',headers:{Range:'bytes=2-8'}}).then(async r=>({status:r.status,length:(await r.arrayBuffer()).byteLength,contentLength:r.headers.get('content-length')}))`
  )
  check(head.status === 206 && head.length === 0 && head.contentLength === '7', 'HEAD response')
  report.head = head
  await remove()
  await remove()
  window.destroy()
  const actual = await create(true)
  report.media = await window.webContents.executeJavaScript(`(async()=>{
   const v=document.createElement('video');v.muted=true;v.preload='auto';v.src=${JSON.stringify(actual.tokenURL)};document.body.append(v);
   const event=name=>new Promise((resolve,reject)=>{const timeout=setTimeout(()=>reject(new Error(name+' timeout')),5000);v.addEventListener(name,()=>{clearTimeout(timeout);resolve()},{once:true});v.addEventListener('error',()=>{clearTimeout(timeout);reject(new Error('decode '+v.error?.code))},{once:true})});
   await event('loadedmetadata');const result=[{event:'metadata',duration:v.duration,seekable:v.seekable.length}];
   await v.play();await new Promise((resolve,reject)=>{const start=performance.now();const tick=()=>{if(v.currentTime>0.2)resolve();else if(performance.now()-start>5000)reject(new Error('time did not advance'));else setTimeout(tick,20)};tick()});v.pause();result.push({event:'time-advanced',time:v.currentTime});
   for(const target of [v.duration*0.1,v.duration*0.5,v.duration*0.9,v.duration*0.05]){const done=event('seeked');v.currentTime=target;await done;if(Math.abs(v.currentTime-target)>0.15)throw new Error('seek mismatch');result.push({event:'seeked',target,time:v.currentTime});}
   v.removeAttribute('src');v.load();v.remove();return result;
  })()`)
  check(report.media.length === 6, 'real playback evidence')
  await remove()
  await remove()
  window.destroy()
  report.pass = true
 } catch (error) {
  report.errors.push(String(error.stack || error))
  report.pass = false
 } finally {
  try {
   await remove?.()
  } catch (error) {
   report.errors.push(String(error))
  }
  if (window && !window.isDestroyed()) window.destroy()
  require('node:fs').writeFileSync(join(__dirname, 'result.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify({pass: report.pass, errors: report.errors}))
  app.exit(report.pass ? 0 : 1)
 }
})
setTimeout(() => app.exit(2), 30000).unref()
