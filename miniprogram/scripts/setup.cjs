const fs = require('node:fs');
const path = require('node:path');
const { prepareComponents } = require('./prepare-components.cjs');

const root = path.resolve(__dirname, '..');
try {
  fs.copyFileSync(
    path.join(root, 'project.config.example.json'),
    path.join(root, 'project.config.json'),
    fs.constants.COPYFILE_EXCL,
  );
  console.log('Created local project.config.json using the tourist AppID.');
} catch (error) {
  if (error.code !== 'EEXIST') throw error;
  console.log('Kept existing local project.config.json.');
}

prepareComponents(root);
