const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const root = path.resolve(__dirname, '..');

function projectFixture(t, withDependency = true) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'printbridge-setup-'));
  t.after(() => {
    assert.equal(path.dirname(fs.realpathSync(directory)), fs.realpathSync(os.tmpdir()));
    assert.ok(path.basename(directory).startsWith('printbridge-setup-'));
    fs.rmSync(directory, { recursive: true, force: true });
  });
  fs.cpSync(path.join(root, 'scripts'), path.join(directory, 'scripts'), { recursive: true });
  fs.copyFileSync(path.join(root, 'project.config.example.json'), path.join(directory, 'project.config.example.json'));
  if (withDependency) {
    fs.cpSync(
      path.join(root, 'node_modules', 'tdesign-miniprogram'),
      path.join(directory, 'node_modules', 'tdesign-miniprogram'),
      { recursive: true },
    );
  }
  return directory;
}

function setup(directory) {
  return spawnSync(process.execPath, [path.join(directory, 'scripts', 'setup.cjs')], { encoding: 'utf8' });
}

test('setup creates runtime components, nested helpers and licenses without changing local configuration', t => {
  const directory = projectFixture(t);
  const localConfig = '{"appid":"test-only-local-appid","setting":{"customLocalSetting":true}}\n';
  fs.writeFileSync(path.join(directory, 'project.config.json'), localConfig);
  const result = setup(directory);
  assert.equal(result.status, 0, result.stderr);
  const runtime = path.join(directory, 'src', 'miniprogram_npm', 'tdesign-miniprogram');
  for (const file of ['input/input.json', 'input/input.js', 'input/input.wxml', 'input/input.wxss',
    'miniprogram_npm/tslib/index.js', 'LICENSE']) {
    assert.ok(fs.existsSync(path.join(runtime, file)), `setup did not prepare ${file}`);
  }
  fs.writeFileSync(path.join(runtime, 'stale-file.txt'), 'previous package version');
  const repeated = setup(directory);
  assert.equal(repeated.status, 0, repeated.stderr);
  assert.ok(!fs.existsSync(path.join(runtime, 'stale-file.txt')), 'old package files were retained');
  assert.equal(fs.readFileSync(path.join(directory, 'project.config.json'), 'utf8'), localConfig);
});

test('setup fails with an actionable error when dependencies have not been installed', t => {
  const directory = projectFixture(t, false);
  const result = setup(directory);
  assert.notEqual(result.status, 0, 'setup claimed success without any runtime components');
  assert.match(result.stderr, /npm ci/);
});
