// scripts/create-build-info.js
const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');

function getCommitSha() {
  try {
    return process.env.COMMIT_SHA || execSync('git rev-parse --short HEAD').toString().trim();
  } catch {
    return 'unknown';
  }
}

function getBuildDate() {
  return process.env.BUILD_DATE || new Date().toISOString();
}

const buildInfo = {
  buildDate: getBuildDate(),
  commitSha: getCommitSha().substr(0, 7)
};

fs.writeFileSync(path.join(__dirname, '../version.json'), JSON.stringify(buildInfo, null, " "));
console.log('Build info module created');
