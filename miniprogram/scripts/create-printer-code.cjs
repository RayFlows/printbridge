const fs = require('node:fs/promises');
const path = require('node:path');
const { parseArgs } = require('node:util');

const maxImageBytes = 2 * 1024 * 1024;

class CodeError extends Error {}

function privateOrigin(value) {
  if (typeof value !== 'string' || !/^https?:\/\/[0-9.]+(?::[0-9]{1,5})?\/?$/.test(value)) {
    throw new CodeError('Multiple network entries must use private IPv4 origins.');
  }
  const host = /^https?:\/\/([0-9.]+)/.exec(value)[1];
  const parts = host.split('.');
  if (parts.length !== 4 || parts.some(part => !/^(0|[1-9][0-9]{0,2})$/.test(part) || Number(part) > 255) ||
    !(Number(parts[0]) === 10 || (Number(parts[0]) === 172 && Number(parts[1]) >= 16 && Number(parts[1]) <= 31) ||
      (Number(parts[0]) === 192 && Number(parts[1]) === 168))) {
    throw new CodeError('Multiple network entries must use private IPv4 origins.');
  }
  try { return new URL(value).origin; } catch { throw new CodeError('Invalid private network origin.'); }
}

function buildCodeRequest({ server, alternateServers, pairingCode, environment = 'release' }) {
  let address;
  try {
    address = new URL(server);
    if (!['http:', 'https:'].includes(address.protocol) || address.username || address.password ||
      address.pathname !== '/' || address.search || address.hash) throw new Error();
    if (!address.hostname.startsWith('[') && address.hostname.split('.').some(label =>
      !label || label.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label)
    )) throw new Error();
  } catch {
    throw new CodeError('Set --server to the HTTP(S) origin reachable by phones.');
  }
  if (typeof pairingCode !== 'string' || !/^[!-~]{32,128}$/.test(pairingCode)) {
    throw new CodeError('Set PRINTBRIDGE_PAIRING_CODE to the code configured on the print server.');
  }
  if (!['release', 'trial', 'develop'].includes(environment)) {
    throw new CodeError('--env must be release, trial or develop.');
  }
  let alternates;
  if (alternateServers !== undefined) {
    if (!Array.isArray(alternateServers) || alternateServers.length < 1 || alternateServers.length > 3) {
      throw new CodeError('Provide one to three --alternate-server values.');
    }
    const primary = privateOrigin(server);
    alternates = alternateServers.map(privateOrigin);
    if (new Set([primary, ...alternates]).size !== alternates.length + 1) {
      throw new CodeError('Network entry origins must be distinct.');
    }
  }
  const entry = { type: 'printbridge', version: 2, server: address.origin, pairingCode,
    ...(alternates ? { alternateServers: alternates } : {}) };
  const pagePath = `pages/connect/index?entry=${encodeURIComponent(JSON.stringify(entry))}`;
  if (pagePath.length > 1024) throw new CodeError('The entry path exceeds the WeChat limit of 1024 characters.');
  return { path: pagePath, width: 430, env_version: environment };
}

async function readImage(response) {
  if (!response.ok) throw new CodeError(`WeChat returned HTTP ${response.status}.`);
  if (!response.body) throw new CodeError('WeChat returned an empty response.');
  if (Number(response.headers.get('content-length')) > maxImageBytes) {
    await response.body.cancel();
    throw new CodeError('WeChat response exceeded the image size limit.');
  }
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > maxImageBytes) throw new CodeError('WeChat response exceeded the image size limit.');
    chunks.push(chunk);
  }
  const data = Buffer.concat(chunks);
  if (data.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) {
    return { data, extension: '.png' };
  }
  if (data.length > 4 && data[0] === 0xff && data[1] === 0xd8 && data[2] === 0xff &&
    data[data.length - 2] === 0xff && data[data.length - 1] === 0xd9) {
    return { data, extension: '.jpg' };
  }
  let code;
  try { code = JSON.parse(data.toString('utf8')).errcode; } catch { /* Not an API JSON response. */ }
  if (Number.isInteger(code)) {
    throw new CodeError(`WeChat returned error ${code}; check the access token, app version and account permissions.`);
  }
  throw new CodeError('WeChat did not return a PNG or JPEG image.');
}

async function createPrinterCode(options, fetchImage = fetch) {
  const body = buildCodeRequest(options);
  if (typeof options.accessToken !== 'string' || !options.accessToken || /\s/.test(options.accessToken)) {
    throw new CodeError('Set WECHAT_ACCESS_TOKEN to a valid mini-program server access token.');
  }
  let image;
  try {
    const response = await fetchImage(
      `https://api.weixin.qq.com/wxa/getwxacode?access_token=${encodeURIComponent(options.accessToken)}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        redirect: 'error',
        signal: AbortSignal.timeout(15000),
      },
    );
    image = await readImage(response);
  } catch (error) {
    if (error instanceof CodeError) throw error;
    throw new CodeError('Could not retrieve the WeChat code image; check the network and retry.');
  }
  const output = `${path.resolve(options.outputBase)}${image.extension}`;
  try {
    await fs.mkdir(path.dirname(output), { recursive: true, mode: 0o700 });
    await fs.writeFile(output, image.data, { flag: 'wx', mode: 0o600 });
  } catch (error) {
    if (error.code === 'EEXIST') throw new CodeError('The output image already exists; choose another --output basename.');
    throw new CodeError('Could not save the code image; check the output directory.');
  }
  return output;
}

async function main() {
  let values;
  try {
    ({ values } = parseArgs({ options: {
      server: { type: 'string' },
      'alternate-server': { type: 'string', multiple: true },
      env: { type: 'string', default: 'release' },
      output: { type: 'string', default: path.resolve(__dirname, '../../data/printer-code') },
    } }));
  } catch {
    throw new CodeError('Usage: npm run printer-code -- --server <origin> [--alternate-server <origin>] [--env trial] [--output <basename>]');
  }
  const output = await createPrinterCode({
    server: values.server,
    alternateServers: values['alternate-server'],
    pairingCode: process.env.PRINTBRIDGE_PAIRING_CODE,
    accessToken: process.env.WECHAT_ACCESS_TOKEN,
    environment: values.env,
    outputBase: values.output,
  });
  console.log(`Printer code saved to ${output}`);
}

if (require.main === module) {
  main().catch(error => {
    console.error(error instanceof CodeError ? error.message : 'Could not generate the printer code.');
    process.exitCode = 1;
  });
}

module.exports = { buildCodeRequest, createPrinterCode };
