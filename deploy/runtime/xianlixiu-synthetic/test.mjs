import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, readdir, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const output = path.join(here, 'dist');
const root = await mkdtemp(path.join(os.tmpdir(), 'ewp-r2-xianlixiu-synthetic-'));
const database = path.join(root, 'synthetic.sqlite');
const sessions = path.join(root, 'sessions');
const children = [];
const failures = new WeakMap();

async function port() {
  const server = createServer();
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const number = server.address().port;
  server.close();
  await once(server, 'close');
  return number;
}

function start(portNumber) {
  const child = spawn(process.execPath, [path.join(output, 'app/server/index.mjs')], {
    cwd: output,
    env: { PATH: process.env.PATH, HOST: '127.0.0.1', PORT: String(portNumber), DATABASE_PATH: database, SESSION_ROOT: sessions, TRUST_PROXY: '0', EWP_SYNTHETIC_ONLY: '1' },
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '';
  child.stderr.on('data', (part) => { stderr = (stderr + part.toString()).slice(-8192); });
  failures.set(child, () => stderr);
  children.push(child);
  return child;
}

async function untilReady(base, child) {
  for (let attempt = 0; attempt < 50; attempt++) {
    if (child.exitCode !== null) throw new Error(`synthetic application exited: ${child.exitCode} ${failures.get(child)()}`);
    try {
      const response = await fetch(base + '/api/health', { signal: AbortSignal.timeout(400) });
      if (response.status === 200) return;
    } catch { /* 等待真实健康状态。 */ }
    await delay(100);
  }
  throw new Error('synthetic application did not become healthy');
}

async function stop(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const killer = setTimeout(() => child.kill('SIGKILL'), 3000).unref();
  child.kill('SIGTERM');
  try { await once(child, 'exit'); } finally { clearTimeout(killer); }
}

try {
  const firstPort = await port();
  const first = start(firstPort);
  const base = `http://127.0.0.1:${firstPort}`;
  await untilReady(base, first);
  assert.equal((await fetch(base + '/api/live')).status, 200);
  assert.equal((await (await fetch(base + '/api/auth/session')).json()).data.authenticated, false);
  assert.equal((await fetch(base + '/api/tools/vip-jit-stock')).status, 401);
  const login = await fetch(base + '/api/auth/login', {
    method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ accessToken: 'DEMO-ONLY' }),
  });
  assert.equal(login.status, 403);
  assert.equal((await login.json()).error.code, 'SYNTHETIC_LOGIN_DISABLED');
  assert.deepEqual(await readdir(sessions), []);
  const homepage = await fetch(base);
  assert.equal(homepage.status, 200);
  assert.match(await homepage.text(), /<html/);

  const second = start(await port());
  const [code] = await Promise.race([once(second, 'exit'), delay(3000).then(() => { throw new Error('second writer was not rejected'); })]);
  assert.notEqual(code, 0);
  assert.equal((await fetch(base + '/api/health')).status, 200);

  const backupTool = path.join(output, 'app/scripts/backup-product-pallet.mjs');
  const backupEnv = { PATH: process.env.PATH, DATABASE_PATH: database };
  const backupResult = execFileSync(process.execPath, [backupTool, 'backup', path.join(root, 'backups')], { env: backupEnv, encoding: 'utf8' });
  const match = backupResult.match(/备份已验证：(.+\.sqlite) SHA-256=([0-9a-f]{64}) 字节=/);
  assert.ok(match, 'synthetic online backup missing digest');
  const restoredPath = path.join(root, 'backups', 'restored.sqlite');
  execFileSync(process.execPath, [backupTool, 'restore', match[1], restoredPath, match[2]], { env: backupEnv });
  const restored = new DatabaseSync(restoredPath, { readOnly: true });
  assert.equal(restored.prepare('PRAGMA integrity_check').get().integrity_check, 'ok');
  assert.equal(restored.prepare('SELECT count(*) AS count FROM product_catalog').get().count, 0);
  restored.close();

  await stop(first);
  const thirdPort = await port();
  const third = start(thirdPort);
  await untilReady(`http://127.0.0.1:${thirdPort}`, third);
  await stop(third);
  console.log('synthetic_original_app=healthy unauthenticated_tools=401 second_writer=denied backup_restore=verified restart=healthy');
} finally {
  for (const child of children) await stop(child);
  await rm(root, { recursive: true, force: true });
}
