import {describe, expect, it} from 'vitest'
import {createRefreshQueue, settleRefreshReads} from './refresh-queue'

function deferred() {
 let resolve!: () => void
 let reject!: (reason: unknown) => void
 const promise = new Promise<void>((ok, fail) => {
  resolve = ok
  reject = fail
 })
 return {promise, resolve, reject}
}
const tick = async () => {
 for (let i = 0; i < 8; i++) await Promise.resolve()
}

describe('task snapshot refresh queue', () => {
 it('waits for slow sibling scans after an API rejects before starting a trailing refresh', async () => {
  const slow = deferred()
  let calls = 0
  const queue = createRefreshQueue(async () => {
   if (++calls === 1) await settleRefreshReads([Promise.reject(new Error('one API failed')), slow.promise])
  })
  queue.setActive(true)
  const first = queue.request()
  const rejected = expect(first).rejects.toThrow('one API failed')
  const final = queue.request()
  await tick()
  expect(calls).toBe(1)
  slow.resolve()
  await rejected
  await final
  expect(calls).toBe(2)
 })
 it('coalesces repeated polling and retains one explicit trailing read without extending the first promise', async () => {
  const first = deferred(),
   second = deferred()
  let calls = 0,
   active = 0,
   max = 0
  const queue = createRefreshQueue(async () => {
   max = Math.max(max, ++active)
   await [first, second][calls++].promise
   active--
  })
  queue.setActive(true)
  const initial = queue.request()
  for (let i = 0; i < 30; i++) expect(queue.request(false)).toBe(initial)
  const event = queue.request()
  expect(queue.request()).toBe(event)
  await tick()
  expect(calls).toBe(1)
  first.resolve()
  await initial
  await tick()
  expect(calls).toBe(2)
  expect(queue.request(false)).toBe(event)
  second.resolve()
  await event
  expect(max).toBe(1)
 })

 it('recovers after a rejected read and still executes the requested final snapshot', async () => {
  const gate = deferred()
  let calls = 0
  const queue = createRefreshQueue(async () => {
   if (++calls === 1) await gate.promise
  })
  queue.setActive(true)
  const first = queue.request()
  const rejected = expect(first).rejects.toThrow('offline')
  const final = queue.request()
  gate.reject(new Error('offline'))
  await rejected
  await final
  await tick()
  await queue.request()
  expect(calls).toBe(3)
 })

 it('invalidates hidden/unmounted results, drops queued work and serializes a new visibility generation', async () => {
  const gate = deferred()
  const applied: number[] = []
  let calls = 0
  const queue = createRefreshQueue(async isCurrent => {
   const call = ++calls
   if (call === 1) await gate.promise
   if (isCurrent()) applied.push(call)
  })
  queue.setActive(true)
  const old = queue.request()
  await tick()
  const dropped = queue.request()
  queue.setActive(false)
  await dropped
  await queue.request()
  expect(calls).toBe(1)
  queue.setActive(true)
  const fresh = queue.request()
  gate.resolve()
  await old
  await fresh
  expect(calls).toBe(2)
  expect(applied).toEqual([2])
 })

 it('reads the latest callback and skips a read disabled before dispatch', async () => {
  let label = 'old'
  const seen: string[] = []
  const queue = createRefreshQueue(async () => {
   seen.push(label)
  })
  queue.setActive(true)
  const skipped = queue.request()
  queue.setActive(false)
  await skipped
  await tick()
  expect(seen).toEqual([])
  label = 'latest'
  queue.setActive(true)
  await queue.request()
  expect(seen).toEqual(['latest'])
 })
})
