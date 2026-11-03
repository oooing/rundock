const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { ChildProcess } = require('node:child_process');

// Observe the options handed to Node, without launching GUI or user programs.
ChildProcess.prototype.spawn = options => options;
require('./node_background.cjs');
const spawn = options => ChildProcess.prototype.spawn.call({}, options);

test('console executables are hidden without changing background lifetime or output', () => {
  const options = {file:process.execPath,detached:true,windowsHide:false,stdio:['ignore',10,11],envPairs:['X=keep']};
  const result = spawn(options);
  assert.equal(result.windowsHide, true);
  assert.equal(result.detached, true);
  assert.equal(result.stdio, options.stdio);
  assert.equal(result.envPairs, options.envPairs);
  assert.equal(options.windowsHide, false);
});

test('PATH lookup honors child environment and cwd with spaces and Chinese', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(),'rundock 中文 path '));
  try {
    fs.copyFileSync(process.execPath,path.join(dir,'fixture.exe'));
    assert.equal(spawn({file:'fixture',cwd:dir,envPairs:['Path='+dir]}).windowsHide,true);
  } finally { fs.rmSync(dir,{recursive:true,force:true}); }
});

test('GUI executables and missing commands keep original options', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(),'rundock-gui-'));
  try {
    const pe = fs.readFileSync(process.execPath);
    pe.writeUInt16LE(2,pe.readUInt32LE(60)+92); // GUI subsystem; never executed.
    const file = path.join(dir,'gui.exe'); fs.writeFileSync(file,pe);
    const gui = {file,detached:true}; assert.equal(spawn(gui),gui);
    const missing = {file:path.join(dir,'missing.exe')}; assert.equal(spawn(missing),missing);
  } finally { fs.rmSync(dir,{recursive:true,force:true}); }
});
