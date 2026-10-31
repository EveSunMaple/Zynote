import {createElement} from 'react'
import {renderToStaticMarkup} from 'react-dom/server'
import {expect, it} from 'vitest'
import {createRefreshQueue} from '@shared/refresh-queue'
import {TaskActions} from './TaskActions'

it('reenables retained task actions after an awaited refresh completes and busy clears', async () => {
 let finish!: () => void
 const response = new Promise<void>(resolve => {
  finish = resolve
 })
 let busy = false
 let savedActions = (currentBusy: boolean) => createElement('button', {disabled: currentBusy}, '继续')
 const queue = createRefreshQueue(async isCurrent => {
  await response
  if (isCurrent()) savedActions = currentBusy => createElement('button', {disabled: currentBusy}, '继续')
 })
 queue.setActive(true)
 const render = () => renderToStaticMarkup(createElement(TaskActions, {actions: savedActions, busy}))
 const run = async () => {
  busy = true
  try {
   await queue.request()
  } finally {
   busy = false
  }
 }
 const operation = run()
 expect(render()).toContain('disabled=""')
 finish()
 await operation
 expect(render()).not.toContain('disabled=')
 expect(render()).toContain('继续')
})
