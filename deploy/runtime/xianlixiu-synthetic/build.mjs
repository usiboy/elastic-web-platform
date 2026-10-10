// Build only original application code + published browser bytes; never copy
// source DB, compressed seed, .runtime, .npmrc, downloads or environment files.
import { cp, lstat, mkdir, readFile, rm } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const source = process.env.XIANLIXIU_SOURCE_DIR;
if (!source || !path.isAbsolute(source)) throw new Error('absolute XIANLIXIU_SOURCE_DIR required');
const pkg = JSON.parse(await readFile(path.join(source, 'package.json'), 'utf8'));
if (pkg.name !== 'xianlixiu-operations-tools') throw new Error('unexpected source project');
const here = path.dirname(fileURLToPath(import.meta.url));
const output = path.join(here, 'dist');
const entry = path.join(source, 'src/server/index.ts');
const browser = path.join(source, 'dist');
const files = ['index.html'];
await rm(output, { recursive: true, force: true });
await mkdir(path.join(output, 'app/server'), { recursive: true, mode: 0o700 });
const esbuildPath = pathToFileURL(path.join(source, 'node_modules/esbuild/lib/main.js')).href;
const { build } = await import(esbuildPath);
await build({
  entryPoints: [entry],
  outfile: path.join(output, 'app/server/index.mjs'),
  bundle: true,
  platform: 'node',
  target: 'node22',
  format: 'esm',
  external: ['node:*'],
  banner: { js: 'import { createRequire as ewpCreateRequire } from "node:module"; const require = ewpCreateRequire(import.meta.url);' },
  sourcemap: false,
  legalComments: 'none',
  logLevel: 'warning',
});
await mkdir(path.join(output, 'app/scripts'), { recursive: true, mode: 0o700 });
await build({
  entryPoints: [path.join(source, 'scripts/backup-product-pallet.ts')],
  outfile: path.join(output, 'app/scripts/backup-product-pallet.mjs'),
  bundle: true,
  platform: 'node',
  target: 'node22',
  format: 'esm',
  external: ['node:*'],
  banner: { js: 'import { createRequire as ewpCreateRequire } from "node:module"; const require = ewpCreateRequire(import.meta.url);' },
  sourcemap: false,
  legalComments: 'none',
  logLevel: 'warning',
});
const assets = path.join(browser, 'assets');
const index = path.join(browser, files[0]);
for (const file of [index, assets]) {
  const info = await lstat(file);
  if (info.isSymbolicLink() || !(info.isFile() || info.isDirectory())) throw new Error('unsafe browser artifact');
}
await mkdir(path.join(output, 'dist'), { mode: 0o700 });
await cp(index, path.join(output, 'dist/index.html'));
await cp(assets, path.join(output, 'dist/assets'), { recursive: true, dereference: false });
await cp(path.join(here, 'node_modules'), path.join(output, 'node_modules'), { recursive: true, dereference: true });
console.log('synthetic_xianlixiu_bundle=server+static_only data=excluded');
