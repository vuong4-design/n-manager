(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.NotionCookieSelection = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function normalizeDomain(domain) {
    return String(domain || '').trim().toLowerCase().replace(/^\./, '');
  }

  function isNotionHostname(hostname) {
    const host = normalizeDomain(hostname);
    return host === 'notion.so' || host.endsWith('.notion.so') ||
      host === 'notion.com' || host.endsWith('.notion.com');
  }

  function domainMatches(hostname, cookie) {
    const host = normalizeDomain(hostname);
    const domain = normalizeDomain(cookie && cookie.domain);
    if (!domain || !isNotionHostname(domain)) return false;
    if (cookie && cookie.hostOnly) return host === domain;
    return host === domain || host.endsWith(`.${domain}`);
  }

  function pathMatches(pathname, cookiePath) {
    const requestPath = pathname || '/';
    const path = cookiePath || '/';
    if (!requestPath.startsWith(path)) return false;
    return path.endsWith('/') || requestPath.length === path.length || requestPath[path.length] === '/';
  }

  function resolveCookieStoreId(tab, stores) {
    const tabId = tab && tab.id;
    for (const store of stores || []) {
      if ((store.tabIds || []).includes(tabId)) return String(store.id);
    }
    if (tab && tab.incognito) {
      const nonDefault = (stores || []).find((store) => String(store.id) !== '0');
      if (nonDefault) return String(nonDefault.id);
    }
    return undefined;
  }

  function cookieAppliesToURL(cookie, tabUrl, storeId) {
    if (!cookie || !tabUrl) return false;
    let url;
    try { url = new URL(tabUrl); } catch (_) { return false; }
    if (!isNotionHostname(url.hostname) || !domainMatches(url.hostname, cookie)) return false;
    if (!pathMatches(url.pathname, cookie.path)) return false;
    if (cookie.secure && url.protocol !== 'https:') return false;
    if (storeId !== undefined && cookie.storeId !== undefined && String(cookie.storeId) !== String(storeId)) return false;
    return true;
  }

  function cookieSpecificity(cookie, tabUrl, storeId) {
    const url = new URL(tabUrl);
    const domain = normalizeDomain(cookie.domain);
    let score = 0;
    if (storeId !== undefined && String(cookie.storeId) === String(storeId)) score += 1000;
    if (domain === normalizeDomain(url.hostname)) score += 300;
    if (cookie.hostOnly) score += 100;
    score += Math.min(90, String(cookie.path || '/').length);
    if (cookie.secure) score += 10;
    return score;
  }

  function dedupeCookies(cookies) {
    const seen = new Set();
    const result = [];
    for (const cookie of cookies || []) {
      const key = [cookie.storeId, cookie.domain, cookie.path, cookie.name, cookie.value].join('|');
      if (seen.has(key)) continue;
      seen.add(key);
      result.push(cookie);
    }
    return result;
  }

  function cookiesForTab(cookies, tabUrl, storeId) {
    return dedupeCookies(cookies)
      .filter((cookie) => cookieAppliesToURL(cookie, tabUrl, storeId))
      .sort((a, b) => cookieSpecificity(b, tabUrl, storeId) - cookieSpecificity(a, tabUrl, storeId));
  }

  function selectNotionTokenCookie(cookies, tabUrl, storeId) {
    return cookiesForTab(cookies, tabUrl, storeId)
      .find((cookie) => cookie.name === 'token_v2' && String(cookie.value || '').length > 0) || null;
  }

  return {
    cookiesForTab,
    isNotionHostname,
    resolveCookieStoreId,
    selectNotionTokenCookie,
  };
});
