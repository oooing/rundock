// RunDock's per-run preload: console grandchildren of detached Node workers
// need windowsHide themselves. Keep detachment, stdio and unref semantics.
// Inspect the PE subsystem so GUI apps (editors, browsers, Electron) are not
// hidden. Unknown executables retain their original behavior.
const { ChildProcess } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
function consoleExecutable(options) {
  const file = options.file;
  if (typeof file !== 'string') return false;
  const env = Object.fromEntries((options.envPairs || []).map(pair => {
    const i = pair.indexOf('=');
    return [pair.slice(0, i).toUpperCase(), pair.slice(i + 1)];
  }));
  const cwd = options.cwd ? String(options.cwd) : process.cwd();
  const directories = /[\\/]/.test(file)
    ? [''] : [cwd, ...(env.PATH || process.env.PATH || '').split(';')];
  const extensions = path.extname(file) ? [''] : ['', '.exe', '.com'];
  for (const directory of directories) {
    for (const extension of extensions) {
      const candidate = path.resolve(cwd, directory.replace(/^"|"$/g, ''), file + extension);
      let fd;
      try {
        fd = fs.openSync(candidate, 'r');
        const dos = Buffer.alloc(64);
        if (fs.readSync(fd, dos, 0, dos.length, 0) !== 64 || dos.readUInt16LE(0) !== 0x5a4d) return false;
        const pe = Buffer.alloc(94);
        if (fs.readSync(fd, pe, 0, pe.length, dos.readUInt32LE(60)) !== pe.length || pe.readUInt32LE(0) !== 0x4550) return false;
        // PE32 and PE32+ share the Subsystem offset.
        return pe.readUInt16LE(92) === 3; // IMAGE_SUBSYSTEM_WINDOWS_CUI
      } catch {
        // Continue PATH lookup; do not change normal spawn errors.
      } finally {
        if (fd !== undefined) fs.closeSync(fd);
      }
    }
  }
  return false;
}
const spawn = ChildProcess.prototype.spawn;
ChildProcess.prototype.spawn = function (options) {
  if (options && !options.windowsHide && consoleExecutable(options)) {
    options = { ...options, windowsHide: true };
  }
  return spawn.call(this, options);
};
