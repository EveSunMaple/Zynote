type Pending = {
 promise: Promise<void>
 resolve: () => void
 reject: (reason: unknown) => void
}

/** A failed sibling must not release the queue while another disk scan remains. */
export async function settleRefreshReads<T extends unknown[]>(reads: {[K in keyof T]: Promise<T[K]>}): Promise<T> {
 const results = await Promise.allSettled(reads)
 return results.map(result => {
  if (result.status === 'rejected') throw result.reason
  return result.value
 }) as T
}

function pending(): Pending {
 let resolve!: () => void
 let reject!: (reason: unknown) => void
 const promise = new Promise<void>((ok, fail) => {
  resolve = ok
  reject = fail
 })
 return {promise, resolve, reject}
}

/** Serializes expensive snapshots. Polls coalesce; explicit refreshes retain one
 * trailing snapshot without extending the promise of the current batch. */
export function createRefreshQueue(read: (isCurrent: () => boolean) => Promise<void>) {
 let active = false
 let generation = 0
 let running: Pending | undefined
 let trailing: Pending | undefined

 function start(batch: Pending) {
  running = batch
  const captured = generation
  void Promise.resolve()
   .then(() => (active && captured === generation ? read(() => active && captured === generation) : undefined))
   .then(batch.resolve, batch.reject)
   .finally(() => {
    running = undefined
    const next = trailing
    trailing = undefined
    if (next) {
     if (active) start(next)
     else next.resolve()
    }
   })
 }

 return {
  setActive(value: boolean) {
   if (active === value) return
   active = value
   generation++
   if (!active && trailing) {
    trailing.resolve()
    trailing = undefined
   }
  },
  request(fresh = true): Promise<void> {
   if (!active) return Promise.resolve()
   if (running) {
    if (!fresh) return running.promise
    trailing ??= pending()
    return trailing.promise
   }
   const batch = pending()
   start(batch)
   return batch.promise
  }
 }
}
