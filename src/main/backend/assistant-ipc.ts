import type {BrowserWindow, IpcMainInvokeEvent} from 'electron'
import {z} from 'zod'
import {localAssistantFailure, type AssistantFailure, type AssistantResult} from '@shared/ipc-result'

export function createAssistantInvoke(
 window: BrowserWindow,
 dispatch: (action: string, args: Record<string, unknown>) => Promise<unknown>,
 failed: (action: string, failure: AssistantFailure) => void
) {
 return async (event: IpcMainInvokeEvent, action: unknown, args: unknown): Promise<AssistantResult> => {
  if (event.sender !== window.webContents || event.senderFrame !== window.webContents.mainFrame)
   throw new Error('拒绝未知页面请求')
  let validated = ''
  try {
   const object = z.record(z.string(), z.unknown()).parse(args || {})
   validated = z.string().min(1).max(100).parse(action)
   if (validated === 'video-ppt:playback-source')
    return {ok: false, error: {code: 'INPUT_INVALID', message: '不允许直接请求内部媒体来源'}}
   return {ok: true, result: await dispatch(validated, object)}
  } catch (reason) {
   const failure =
    reason instanceof z.ZodError
     ? {code: 'INPUT_INVALID', message: '请求参数无效，请核对后重试'}
     : localAssistantFailure(reason)
   failed(validated, failure)
   return {ok: false, error: failure}
  }
 }
}
