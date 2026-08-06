const assert = require('node:assert/strict');
const {
  cookiesForTab,
  isNotionHostname,
  resolveCookieStoreId,
  selectNotionTokenCookie,
} = require('../cookie-selection.js');

assert.equal(isNotionHostname('www.notion.so'), true);
assert.equal(isNotionHostname('app.notion.com'), true);
assert.equal(isNotionHostname('notion.example.com'), false);

const stores = [
  { id: '0', tabIds: [1] },
  { id: '1', tabIds: [9] },
];
assert.equal(resolveCookieStoreId({ id: 9, incognito: true }, stores), '1');
assert.equal(resolveCookieStoreId({ id: 1, incognito: false }, stores), '0');
assert.equal(resolveCookieStoreId({ id: 7, incognito: true }, stores), '1');

const cookies = [
  { name: 'token_v2', value: 'wrong-store', domain: '.notion.so', path: '/', secure: true, storeId: '0' },
  { name: 'token_v2', value: 'broad', domain: '.notion.so', path: '/', secure: true, storeId: '1' },
  { name: 'token_v2', value: 'specific', domain: 'www.notion.so', path: '/workspace', secure: true, hostOnly: true, storeId: '1' },
  { name: 'device_id', value: 'device', domain: '.notion.so', path: '/', secure: true, storeId: '1' },
  { name: 'token_v2', value: 'other-domain', domain: '.example.com', path: '/', secure: true, storeId: '1' },
];
const url = 'https://www.notion.so/workspace/page';
assert.equal(selectNotionTokenCookie(cookies, url, '1').value, 'specific');
assert.deepEqual(cookiesForTab(cookies, url, '1').map((cookie) => cookie.value), ['specific', 'broad', 'device']);
assert.equal(selectNotionTokenCookie(cookies, 'https://app.notion.com/', '1'), null);

console.log('cookie-selection tests passed');
