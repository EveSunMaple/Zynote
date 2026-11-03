import {contextBridge, ipcRenderer, webUtils} from 'electron'
import type {EventPayload, Bridge} from '@shared/types'
import {unwrapAssistantResult} from '@shared/ipc-result'
const bridge: Bridge = {
 invoke: async (action, args) => unwrapAssistantResult(await ipcRenderer.invoke('assistant:invoke', action, args)),
 onEvent(callback) {
  const handler = (_event: Electron.IpcRendererEvent, data: EventPayload) => callback(data)
  ipcRenderer.on('assistant:event', handler)
  return () => ipcRenderer.removeListener('assistant:event', handler)
 },
 filePath: file => webUtils.getPathForFile(file),
 platform: process.platform
}
contextBridge.exposeInMainWorld('assistant', bridge)
