// 仅用于受限 ECS 测试仓库 Vite 构建停在 transforming 时的无凭据诊断。
// 在相同容器的 loopback Node inspector 中取 3 秒 CPU 样本，不改源码、不上传密钥。
const endpoint = await fetch('http://127.0.0.1:9229/json/list')
const targets = await endpoint.json()
if (!endpoint.ok || targets.length !== 1) throw new Error('No single local inspector target')

const ws = new WebSocket(targets[0].webSocketDebuggerUrl)
const pending = new Map()
let nextID = 1

ws.addEventListener('message', (event) => {
  const response = JSON.parse(event.data)
  const resolve = pending.get(response.id)
  if (resolve) {
    pending.delete(response.id)
    resolve(response)
  }
})

await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true })
  ws.addEventListener('error', reject, { once: true })
})

async function call(method) {
  const id = nextID++
  ws.send(JSON.stringify({ id, method }))
  return await Promise.race([
    new Promise((resolve) => pending.set(id, resolve)),
    new Promise((_, reject) => setTimeout(() => reject(new Error(`Inspector ${method} timeout`)), 10000)),
  ])
}

await call('Profiler.enable')
await call('Profiler.start')
await new Promise((resolve) => setTimeout(resolve, 3000))
const response = await call('Profiler.stop')
if (!response.result?.profile) throw new Error('No CPU profile')
const profile = response.result.profile
const top = profile.nodes
  .filter((node) => node.hitCount > 0)
  .sort((a, b) => b.hitCount - a.hitCount)
  .slice(0, 12)
  .map((node) => ({ hits: node.hitCount, function: node.callFrame.functionName, url: node.callFrame.url, line: node.callFrame.lineNumber }))
console.log(JSON.stringify({ samples: profile.samples?.length, top }, null, 2))
ws.close()
