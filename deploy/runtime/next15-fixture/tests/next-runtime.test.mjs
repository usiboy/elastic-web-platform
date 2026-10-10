import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));

async function availablePort() {
  const socket = createServer();
  socket.listen(0, '127.0.0.1');
  await once(socket, 'listening');
  const { port } = socket.address();
  socket.close();
  await once(socket, 'close');
  return port;
}

test('Next 15.5.27 standalone serves request-time SSR, API, static JS and chunks', { timeout: 25_000 }, async () => {
  const port = await availablePort();
  const server = spawn(process.execPath, ['.next/standalone/server.js'], {
    cwd: root,
    env: { ...process.env, HOSTNAME: '127.0.0.1', PORT: String(port), NODE_ENV: 'production' },
    stdio: 'ignore',
  });
  const base = `http://127.0.0.1:${port}`;
  try {
    let ready = false;
    for (let i = 0; i < 50; i++) {
      if (server.exitCode !== null) break;
      try {
        const response = await fetch(`${base}/api/hello`, { signal: AbortSignal.timeout(500) });
        ready = response.ok;
        if (ready) break;
      } catch { /* 等待真实监听状态，避免启动竞态。 */ }
      await delay(100);
    }
    assert.equal(ready, true, 'standalone server did not become ready');
    const api = await fetch(`${base}/api/hello`);
    assert.equal(api.status, 200);
    const expected = { service: 'r2-next-synthetic', node: process.versions.node };
    if (process.env.EWP_SYNTHETIC_VARIANT === 'v3') expected.variant = 'v3';
    assert.deepEqual(await api.json(), expected);
    const health = await fetch(`${base}/api/health`);
    assert.equal(health.status, 200);
    assert.deepEqual(await health.json(), { ok: true, service: 'r2-next-synthetic' });
    const first = await (await fetch(base)).text();
    const second = await (await fetch(base)).text();
    assert.match(first, /R2 合成 SSR/);
    const marker = /data-request-id="([0-9a-f-]{36})"/;
    assert.notEqual(first.match(marker)?.[1], second.match(marker)?.[1], 'SSR must run per request');
    const js = first.match(/src="(\/_next\/static\/[^"]+\.js)"/)?.[1];
    assert.ok(js, 'SSR page did not reference a static JS asset');
    const asset = await fetch(`${base}${js}`);
    assert.equal(asset.status, 200);
    assert.match(asset.headers.get('content-type'), /javascript/);
    const response = await fetch(`${base}/api/stream`);
    assert.equal(response.status, 200);
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    assert.equal(decoder.decode((await reader.read()).value), 'start\n');
    assert.equal(decoder.decode((await reader.read()).value), 'done\n');
  } finally {
    if (server.exitCode === null && server.signalCode === null) {
      const killer = setTimeout(() => server.kill('SIGKILL'), 3_000).unref();
      server.kill('SIGTERM');
      try {
        await once(server, 'exit');
      } finally {
        clearTimeout(killer);
      }
    }
  }
});
