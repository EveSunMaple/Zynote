const {app, BrowserWindow, protocol} = require('electron')
app.on('window-all-closed', () => {})
const {join} = require('node:path')
const fs = require('node:fs/promises')
const {createHash} = require('node:crypto')
const {ManagedPlayback} = require('./bundle/media/managed-playback.js')
const {installMediaProtocol, mediaResponse} = require('./bundle/media/media-protocol.js')
const {GoCore} = require('./bundle/backend/go-core.js')
const http = require('node:http')
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
let window, remove, manager, core, server
let providerCalls = 0
function check(value, message) {
 if (!value) throw new Error(message)
}
app.whenReady().then(async () => {
 try {
  const codec = process.env.ZYNOTE_MEDIA_SMOKE_CODEC
  const benchmark = process.env.ZYNOTE_BOUND_BENCHMARK === '1'
  const runLabel = 'fixture'
  const seed = JSON.parse((await fs.readFile(join(__dirname, 'seed.json'), 'utf8')).replace(/^\uFEFF/, ''))
  const library = seed.libraryRoot
  const bytes = await fs.readFile(seed.managedPath)
  const sha = createHash('sha256').update(bytes).digest('hex')
  report.fixture = {
   size: bytes.length,
   sha256: sha,
   codec,
   origin: 'MIT 6.0001 existing 60–100s excerpt; WebM controlled derivative, original matching SRT reused'
  }
  server = http.createServer((request, response) => {
   let body = ''
   request.on('data', chunk => {
    body += chunk
    if (body.length > 1000000) request.destroy()
   })
   request.on('end', () => {
    providerCalls++
    check(body.includes('First name Anna, last name Belle.'), 'provider request must contain original cue')
    response.setHeader('content-type', 'application/json')
    response.end(
     JSON.stringify({
      choices: [
       {
        finish_reason: 'stop',
        message: {
         content: JSON.stringify([
          {
           cue: 0,
           quote: 'First name Anna, last name Belle.',
           point: 'Controlled fixture point anchored to the original cue.',
           topic: 'Fixture'
          }
         ])
        }
       }
      ]
     })
    )
   })
  })
  await new Promise(done => server.listen(0, '127.0.0.1', done))
  const data = join(__dirname, 'data-' + runLabel),
   resources = join(__dirname, 'resources-' + runLabel)
  await fs.mkdir(data)
  await fs.mkdir(resources)
  await fs.writeFile(
   join(data, 'settings.json'),
   JSON.stringify({
    profiles: [],
    activeProfile: '',
    theme: 'light',
    prompts: {},
    qwenProfile: {
     id: 'qwen-default',
     name: 'isolated local provider',
     provider: 'qwen',
     baseUrl: 'http://127.0.0.1:' + server.address().port + '/v1',
     model: 'fixture-local',
     apiFormat: 'chat_completions',
     encryptedKey: 'controlled-fixture-only'
    }
   })
  )
  core = new GoCore({
   appVersion: '0.0.1',
   dataRoot: data,
   configRoot: data,
   appRoot: __dirname,
   libraryRoot: library,
   previousLibraryRoot: '',
   resourcesRoot: resources,
   promptsRoot: resources,
   nativePath: '',
   portable: false,
   services: {'crypto.unprotect': async () => ({text: 'fixture-local-only'})},
   onEvent: () => {},
   onFailed: reason => report.errors.push('core:' + reason)
  })
  core.start()
  for (let i = 0; i < 800 && core.currentStatus !== 'ready'; i++) await new Promise(done => setTimeout(done, 10))
  check(core.currentStatus === 'ready', 'real Go handshake')
  await core.call('db:bootstrap', {})
  let boundInput, summaryRequest
  if (!benchmark) {
   summaryRequest = {
    operationId: 'dddddddd-dddd-4ddd-8ddd-dddddddddddd',
    mode: 'brief',
    retention: {videoTaskID: seed.videoTaskID, expectedRetentionReceiptSHA256: seed.receiptSHA256}
   }
   const summary = await core.call('video-ppt:summary-from-retained', summaryRequest)
   check(
    summary.count === 1 && summary.sourceBinding?.summaryTaskID === summaryRequest.operationId,
    'real bound summary public response'
   )
   await core.call('video-ppt:summary-from-retained', summaryRequest)
   check(providerCalls === 1, 'same-id retry must not replay local provider')
   boundInput = {
    requestID,
    summaryTaskID: summary.sourceBinding.summaryTaskID,
    itemIndex: 0,
    expectedSourceBindingSHA256: summary.sourceBinding.sourceBindingSHA256
   }
   const declared = await core.call('video-ppt:bound-playback-source', {
    summaryTaskID: boundInput.summaryTaskID,
    itemIndex: 0,
    expectedSourceBindingSHA256: boundInput.expectedSourceBindingSHA256
   })
   check(declared.taskID === seed.videoTaskID && declared.sourceSHA256 === sha, 'real private bound declaration')
   report.business = {
    providerCalls,
    retentionFactory: true,
    publicSummary: true,
    sameIDNoReplay: true,
    privateDeclaration: true,
    sourceBinding: summary.sourceBinding,
    authoritySHA256: declared.bindingAuthoritySHA256
   }
  } else {
   boundInput = seed.boundInput
   report.benchmark = {
    srtBytes: seed.srtBytes,
    cueCount: seed.cueCount,
    journalBytes: seed.journalBytes,
    providerCalls: 0
   }
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
   manager = new ManagedPlayback(
    library,
    async () => {
     throw new Error('A stub forbidden')
    },
    undefined,
    (action, input) => core.callTracked(action, input)
   )
   remove = installMediaProtocol(window.webContents.session, window, manager, undefined)
   await window.loadFile(join(__dirname, 'page.html'))
   return manager.openBound(boundInput)
  }
  if (benchmark) {
   const owners = [],
    inputs = [boundInput, {...boundInput, requestID: '9e326025-30b4-45d0-bd27-4a30275adcc2'}],
    rpc = []
   let active = 0,
    maximum = 0
   const tracked = (action, input) => {
    const begin = performance.now(),
     call = core.callTracked(action, input)
    active++
    maximum = Math.max(maximum, active)
    void call.physicalSettled.then(() => {
     active--
     rpc.push({action, physicalMs: performance.now() - begin})
    })
    return call
   }
   const tokens = []
   for (const input of inputs) {
    const owner = new ManagedPlayback(
     library,
     async () => {
      throw new Error('A stub forbidden')
     },
     undefined,
     tracked
    )
    owners.push(owner)
    tokens.push(await owner.openBound(input))
   }
   const sequential = []
   for (let i = 0; i < 5; i++) {
    const begin = performance.now(),
     response = await mediaResponse(owners[0], new Request(tokens[0].tokenURL, {method: 'HEAD'}))
    check(response.status === 200, 'sequential HEAD')
    sequential.push(performance.now() - begin)
   }
   const jobs = Array.from({length: 8}, (_, i) => i),
    latency = []
   const begin = performance.now()
   while (jobs.length) {
    await Promise.all(
     jobs.splice(0, 4).map(async i => {
      const owner = i % 2,
       start = (i * 333337) % Math.max(1, bytes.length - 32),
       tick = performance.now(),
       response = await mediaResponse(
        owners[owner],
        new Request(tokens[owner].tokenURL, {headers: {range: 'bytes=' + start + '-' + (start + 31)}})
       )
      check(response.status === 206, 'seek response')
      check(Buffer.from(await response.arrayBuffer()).equals(bytes.subarray(start, start + 32)), 'seek exact bytes')
      latency.push(performance.now() - tick)
     })
    )
   }
   report.benchmark = {
    ...report.benchmark,
    sequentialHeadMs: sequential,
    eightSeekWallMs: performance.now() - begin,
    seekMs: latency,
    maximumPhysicalRPCs: maximum,
    rpc
   }
   check(maximum <= 4 && rpc.length === 25, 'bounded physical checks count')
   for (const owner of owners) await owner.dispose()
   report.pass = true
   return
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
  const headStatus = async tokenURL =>
   window.webContents.executeJavaScript(`fetch(${JSON.stringify(tokenURL)},{method:'HEAD'}).then(r=>r.status)`)
  const rewriteKeepingStat = async (path, value, tokenURL) => {
   // Normalize the fixture before observing a successful baseline: Date-based
   // utimes cannot round-trip arbitrary NTFS sub-millisecond timestamps.
   const stableTime = new Date(1700000000000)
   await fs.utimes(path, stableTime, stableTime)
   check((await headStatus(tokenURL)) === 200, 'normalized same-stat baseline')
   const before = await fs.stat(path),
    original = await fs.readFile(path)
   check(
    value.length === original.length && !value.equals(original),
    'negative fixture must change bytes at same length'
   )
   await fs.writeFile(path, value)
   await fs.utimes(path, before.atime, before.mtime)
   const after = await fs.stat(path)
   check(after.size === before.size && after.mtimeMs === before.mtimeMs, 'negative fixture must preserve size/mtime')
   return async () => {
    await fs.writeFile(path, original)
    await fs.utimes(path, before.atime, before.mtime)
   }
  }
  const srtBytes = await fs.readFile(seed.savedSubtitle)
  const restoreSRT = await rewriteKeepingStat(
   seed.savedSubtitle,
   Buffer.from(srtBytes.toString('utf8').replace('First name', 'FIRSt name')),
   actual.tokenURL
  )
  const changedSRTStatus = await headStatus(actual.tokenURL)
  check(changedSRTStatus === 404, 'changed same-stat SRT must reject current token')
  await restoreSRT()
  const afterSRT = await manager.openBound({...boundInput, requestID: '9e326025-30b4-45d0-bd27-4a30275adcc4'})
  check((await headStatus(afterSRT.tokenURL)) === 200, 'explicit reopen after exact restoration')
  const authorityPath = join(library, 'NoteGeneration', boundInput.summaryTaskID + '.playback-binding.json')
  const authority = JSON.parse(await fs.readFile(authorityPath, 'utf8'))
  const reordered = Buffer.from(
   JSON.stringify(
    Object.fromEntries(
     Object.keys(authority)
      .sort((first, second) => first.localeCompare(second))
      .map(key => [key, authority[key]])
    )
   )
  )
  const restoreAuthority = await rewriteKeepingStat(authorityPath, reordered, afterSRT.tokenURL)
  const changedAuthorityStatus = await headStatus(afterSRT.tokenURL)
  check(changedAuthorityStatus === 404, 'same-stat authority bytes must reject current token')
  await restoreAuthority()
  const afterAuthority = await manager.openBound({...boundInput, requestID: '9e326025-30b4-45d0-bd27-4a30275adcc5'})
  await fs.rename(seed.managedPath, seed.managedPath + '.held')
  const missingMediaStatus = await headStatus(afterAuthority.tokenURL)
  check(missingMediaStatus === 404, 'missing current media path must reject held token')
  await fs.rename(seed.managedPath + '.held', seed.managedPath)
  await core.call('video-ppt:summary-from-retained', summaryRequest)
  check(providerCalls === 1, 'negative live checks must preserve same-ID text recovery without provider replay')
  report.negative = {
   changedSRTStatus,
   changedAuthorityStatus,
   missingMediaStatus,
   sameSizeMtime: true,
   textRecoveryNoReplay: true
  }
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
   report.pass = false
  }
  try {
   if (window && !window.isDestroyed()) window.destroy()
   core?.dispose()
   if (server) await new Promise((done, reject) => server.close(error => (error ? reject(error) : done())))
  } catch (error) {
   report.errors.push(String(error))
   report.pass = false
  }
  require('node:fs').writeFileSync(join(__dirname, 'result.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify({pass: report.pass, errors: report.errors}))
  app.exit(report.pass ? 0 : 1)
 }
})
setTimeout(() => app.exit(2), 30000).unref()
