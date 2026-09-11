// Keep Vite's output attached to files, not PowerShell-owned redirect pipes.
// The latter can leave esbuild requests pending after the launcher exits.
const { spawn } = require('node:child_process');
const { openSync, closeSync } = require('node:fs');

const [cwd, stdout, stderr, ...args] = process.argv.slice(2);
const out = openSync(stdout, 'a');
const err = openSync(stderr, 'a');
const child = spawn(process.execPath, args, {
  cwd,
  detached: true,
  windowsHide: true,
  stdio: ['ignore', out, err],
});
closeSync(out);
closeSync(err);
child.on('error', (error) => {
  console.error(error.message);
  process.exitCode = 1;
});
child.on('spawn', () => {
  console.log(child.pid);
  child.unref();
});
