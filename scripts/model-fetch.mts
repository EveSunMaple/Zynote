import {EnvHttpProxyAgent, fetch as proxyFetch} from 'undici'

// Node's global fetch ignores HTTP(S)_PROXY. Model acquisition must follow the
// same proxy/NO_PROXY environment as curl so restricted networks can download.
// HTTP/2 through public mirrors/proxies frequently truncates large downloads.
// Restricted networks can also set HF_ENDPOINT=https://hf-mirror.com; add the
// mirror and its redirect hosts (hf-mirror.com, hf.co, xethub.hf.co) to NO_PROXY
// when those hosts are reachable directly.
const dispatcher = new EnvHttpProxyAgent({allowH2: false})

export async function modelFetch(input: string | URL, init: RequestInit = {}, attempts = 3): Promise<Response> {
 let failure: unknown
 for (let attempt = 1; attempt <= attempts; attempt++) {
  try {
   // biome-ignore format: NOSONAR must stay on the await line
   const response = await proxyFetch(input, {...init, dispatcher} as never) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   if (response.status !== 429 && response.status < 500) return response as unknown as Response
   failure = new Error(`模型服务暂时不可用：HTTP ${response.status}`)
   // biome-ignore format: NOSONAR must stay on the await line
   await response.body?.cancel() // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  } catch (error) {
   failure = error
  }
  // biome-ignore format: NOSONAR must stay on the await line
  if (attempt < attempts) await new Promise(resolve => setTimeout(resolve, 500 * 2 ** attempt)) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
 }
 throw failure instanceof Error ? failure : new Error('模型服务连接失败')
}
