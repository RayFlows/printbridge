const fs = require('node:fs');
const path = require('node:path');

function prepareComponents(projectRoot) {
  const root = fs.realpathSync(projectRoot);
  const packageRoot = path.join(root, 'node_modules', 'tdesign-miniprogram');
  const manifestFile = path.join(packageRoot, 'package.json');
  if (!fs.existsSync(manifestFile)) {
    throw new Error('TDesign is not installed. Run npm ci --ignore-scripts, then npm run setup.');
  }
  const manifest = JSON.parse(fs.readFileSync(manifestFile, 'utf8'));
  if (typeof manifest.miniprogram !== 'string') {
    throw new Error('TDesign no longer publishes a native mini-program directory; review the dependency update.');
  }
  const source = path.resolve(packageRoot, manifest.miniprogram);
  if (!source.startsWith(packageRoot + path.sep) ||
    !fs.existsSync(path.join(source, 'input', 'input.json')) ||
    !fs.existsSync(path.join(source, 'miniprogram_npm', 'tslib', 'index.js'))) {
    throw new Error('TDesign native package is incomplete. Run npm ci --ignore-scripts and retry.');
  }

  // TDesign publishes ready-to-use native components, including its npm helpers.
  // Keep this specific to that distribution; it is not a general npm bundler.
  const outputRoot = path.join(root, 'src', 'miniprogram_npm');
  const target = path.join(outputRoot, 'tdesign-miniprogram');
  for (const directory of [path.join(root, 'src'), outputRoot, target]) {
    if (fs.existsSync(directory) &&
      (fs.lstatSync(directory).isSymbolicLink() || fs.realpathSync(directory) !== directory)) {
      throw new Error('Component output must stay inside the project without symbolic links.');
    }
  }
  fs.mkdirSync(outputRoot, { recursive: true });
  if (fs.realpathSync(outputRoot) !== outputRoot || path.dirname(target) !== outputRoot) {
    throw new Error('Component output is outside the intended project directory.');
  }
  fs.rmSync(target, { recursive: true, force: true });
  fs.cpSync(source, target, { recursive: true });
  fs.copyFileSync(path.join(packageRoot, 'LICENSE'), path.join(target, 'LICENSE'));
  console.log(`Prepared tdesign-miniprogram ${manifest.version} in src/miniprogram_npm.`);
}

if (require.main === module) prepareComponents(path.resolve(__dirname, '..'));

module.exports = { prepareComponents };
