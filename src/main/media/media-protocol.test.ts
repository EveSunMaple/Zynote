import {EventEmitter} from 'node:events'
import type {BrowserWindow, Session} from 'electron'
import {describe, expect, it, vi} from 'vitest'
import {mediaRange, installMediaProtocol} from './media-protocol'
import type {ManagedPlayback} from './managed-playback'
describe('single media range parser', () => {
 it('handles complete, closed, suffix, open-ended and EOF clamp with exact intervals', () => {
  expect(mediaRange(null, 100)).toEqual({start: 0, end: 99, partial: false})
  for (const [value, start, end] of [
   ['bytes=0-0', 0, 0],
   ['bytes=8-', 8, 99],
   ['bytes=-9', 91, 99],
   ['bytes=-999', 0, 99],
   ['bytes=8-999', 8, 99]
  ] as const)
   expect(mediaRange(value, 100)).toEqual({start, end, partial: true})
  for (const value of [
   'bytes=100-',
   'bytes=9-2',
   'bytes=-0',
   'bytes=-',
   'bytes=0-1,3-4',
   'bytes=1.5-3',
   'bytes=0-9007199254740992',
   'Bytes=0-1',
   'bytes= 0-1',
   'x'.repeat(129)
  ])
   expect(mediaRange(value, 100)).toBeUndefined()
 })
})
describe('media owner and cleanup gates', () => {
 it('fails closed for destroyed owner and unhandles/disposes exactly once even if unhandle throws', async () => {
  let destroyed = false
  const contents = Object.assign(new EventEmitter(), {id: 7, mainFrame: {}, isDestroyed: () => destroyed})
  const owner = Object.assign(new EventEmitter(), {webContents: contents, isDestroyed: () => destroyed})
  const onBeforeRequest = vi.fn(),
   unhandle = vi.fn(() => {
    throw new Error('controlled unhandle race')
   }),
   handle = vi.fn()
  const session = {webRequest: {onBeforeRequest}, protocol: {handle, unhandle}}
  const manager = {dispose: vi.fn(async () => {}), closeAll: vi.fn()}
  const remove = installMediaProtocol(
   session as unknown as Session,
   owner as unknown as BrowserWindow,
   manager as unknown as ManagedPlayback,
   undefined
  )
  const gate = onBeforeRequest.mock.calls[0][1],
   callback = vi.fn()
  gate({webContentsId: 7, frame: contents.mainFrame}, callback)
  expect(callback).toHaveBeenLastCalledWith({cancel: false})
  gate({webContentsId: 8, frame: contents.mainFrame}, callback)
  expect(callback).toHaveBeenLastCalledWith({cancel: true})
  contents.emit('did-start-navigation', {}, 'about:srcdoc', false, false)
  expect(manager.closeAll).not.toHaveBeenCalled()
  contents.emit('did-start-navigation', {}, 'file:///replacement', false, true)
  expect(manager.closeAll).toHaveBeenCalledTimes(1)
  destroyed = true
  Object.defineProperty(contents, 'mainFrame', {
   get() {
    throw new Error('destroyed getter must not be read')
   }
  })
  gate({webContentsId: 7}, callback)
  expect(callback).toHaveBeenLastCalledWith({cancel: true})
  await remove()
  await remove()
  owner.emit('closed')
  expect(unhandle).toHaveBeenCalledTimes(1)
  expect(manager.dispose).toHaveBeenCalledTimes(1)
  expect(contents.listenerCount('did-start-navigation')).toBe(0)
  expect(onBeforeRequest).toHaveBeenLastCalledWith(null)
 })
})
