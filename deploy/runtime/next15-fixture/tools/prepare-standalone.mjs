import { cpSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
cpSync(`${root}.next/static`, `${root}.next/standalone/.next/static`, { recursive: true });
