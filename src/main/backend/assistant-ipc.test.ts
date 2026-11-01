import type {BrowserWindow, IpcMainInvokeEvent} from 'electron'
import {describe, expect, it, vi} from 'vitest'
import {createAssistantInvoke} from './assistant-ipc'

function fixture() {
 const frame = {}
 const webContents = {mainFrame: frame}
 const window = {webContents} as BrowserWindow
 const event = {sender: webContents, senderFrame: frame} as IpcMainInvokeEvent
 return {window, event}
}
describe('assistant main IPC handler', () => {
 it('rejects unknown senders and subframes before dispatch or envelope', async () => {
  const {window, event} = fixture()
  const dispatch = vi.fn(),
   failed = vi.fn()
  const invoke = createAssistantInvoke(window, dispatch, failed)
  await expect(invoke({...event, sender: {}} as IpcMainInvokeEvent, 'test', {})).rejects.toThrow('拒绝未知页面请求')
  await expect(invoke({...event, senderFrame: {}} as IpcMainInvokeEvent, 'test', {})).rejects.toThrow(
   '拒绝未知页面请求'
  )
  expect(dispatch).not.toHaveBeenCalled()
  expect(failed).not.toHaveBeenCalled()
 })
 it('validates input, preserves success and ordinary shell messages, and exposes only bounded error fields', async () => {
  const {window, event} = fixture()
  const dispatch = vi
   .fn()
   .mockResolvedValueOnce(null)
   .mockResolvedValueOnce({nested: ['raw']})
   .mockRejectedValueOnce(new Error('仅支持B站链接'))
  const failed = vi.fn()
  const invoke = createAssistantInvoke(window, dispatch, failed)
  expect(await invoke(event, 'test', {})).toEqual({ok: true, result: null})
  expect(await invoke(event, 'test', {})).toEqual({ok: true, result: {nested: ['raw']}})
  expect(await invoke(event, 'test', {})).toEqual({
   ok: false,
   error: {code: 'REQUEST_FAILED', message: '仅支持B站链接'}
  })
  expect(await invoke(event, '', {})).toEqual({
   ok: false,
   error: {code: 'INPUT_INVALID', message: '请求参数无效，请核对后重试'}
  })
  expect(dispatch).toHaveBeenCalledTimes(3)
 })
})

it('rejects a raw internal playback action from even the trusted main frame before dispatch', async () => {
 const {window, event} = fixture(),
  dispatch = vi.fn(),
  failed = vi.fn()
 const invoke = createAssistantInvoke(window, dispatch, failed)
 expect(await invoke(event, 'video-ppt:playback-source', {id: 'ignored'})).toEqual({
  ok: false,
  error: {code: 'INPUT_INVALID', message: '不允许直接请求内部媒体来源'}
 })
 expect(dispatch).not.toHaveBeenCalled()
})

it.each([
 'video-ppt:bound-playback-source',
 'video-ppt:prepare-playback-release',
 'video-ppt:finalize-playback-release'
])('denies private release action %s before dispatch', async action => {
 const dispatch = vi.fn(async () => null)
 const webContents = {mainFrame: {}}
 const invoke = createAssistantInvoke({webContents} as never, dispatch, vi.fn())
 const result = await invoke({sender: webContents, senderFrame: webContents.mainFrame} as never, action, {})
 expect(result).toMatchObject({ok: false, error: {code: 'INPUT_INVALID'}})
 expect(dispatch).not.toHaveBeenCalled()
})
