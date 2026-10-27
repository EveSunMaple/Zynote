import type {ApplicationContext} from '@main/app/context'
import type {IpcHandler} from '@main/ipc/handler'

export function createWindowHandler(context: Pick<ApplicationContext, 'window'>): IpcHandler {
 return {
  actions: new Set(['window:minimize', 'window:toggle-maximize', 'window:close', 'window:is-maximized']),
  invoke(action: string) {
   const {window} = context
   switch (action) {
    case 'window:minimize':
     window.minimize()
     return Promise.resolve(null)
    case 'window:toggle-maximize':
     if (window.isMaximized()) window.unmaximize()
     else window.maximize()
     return Promise.resolve(window.isMaximized())
    case 'window:close':
     window.close()
     return Promise.resolve(null)
    case 'window:is-maximized':
     return Promise.resolve(window.isMaximized())
    default:
     throw new Error('未知操作')
   }
  }
 }
}
