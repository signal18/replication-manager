import { adminMode, ADMIN_REQUIRED_HEADER } from './adminMode'

const toBase64 = (str) => btoa(unescape(encodeURIComponent(str)));

const authConfig = {
  local: { // Local calls
    resolveUrl: (apiUrl) => `/api/${apiUrl}`,
    getToken: () => localStorage.getItem('user_token')
  },
  public: { // Local public calls (no auth header)
    resolveUrl: (apiUrl) => `/api/${apiUrl}`,
    getToken: () => null
  },
  peer: { // Peer calls
    resolveUrl: (apiUrl, baseUrl) => `/peer/${baseUrl}/api/${apiUrl}`,
    getToken: (baseUrl) => {
      if (baseUrl == "") {
        return localStorage.getItem(`user_token`);
      } else {
        return localStorage.getItem(`user_token_${baseUrl}`);
      }
    }
  },
  meet: { // Mattermost API
    //resolveUrl: (apiUrl) => `https://meet.signal18.io/api/v4/${apiUrl}`,
    resolveUrl: (apiUrl) => `/meet/${apiUrl}`,
    getToken: () => localStorage.getItem('user_token')
  }
};

const getContentType = (type) => type === 'json' ? { 'Content-Type': 'application/json; charset="utf-8"' } : {};

const buildHeaders = (authValue, contentType, baseUrl) => {
  const encodedBaseUrl = baseUrl.length > 0 ? toBase64(baseUrl) : "";
  const { getToken } = authConfig[authValue] || {};
  const token = getToken ? getToken(encodedBaseUrl) : null;
  //const userID = localStorage.getItem('userID');

  return {
    ...getContentType(contentType),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    //...(userID ? { 'X-User-ID': userID } : {}),
    Accept: '*/*',
  };
};

const resolveUrl = (apiUrl, authValue, baseUrl) => {
  const encodedBaseUrl = toBase64(baseUrl);
  const { resolveUrl } = authConfig[authValue] || {};
  return resolveUrl ? resolveUrl(apiUrl, encodedBaseUrl) : apiUrl;
};

const handleResponse = async (response) => {
  const contentType = response.headers.get('Content-Type')
  let data = null
  if (contentType && contentType.includes('application/json')) {
    data = await response.json()
  } else if (contentType && contentType.includes('text/plain')) {
    data = await response.text()
    if (data.startsWith('[') || data.startsWith('{')) {
      try {
        data = JSON.parse(data);
      } catch (e) {
        throw new Error("Failed to parse JSON: " + e.message);
      }
    }
  }
  else if (contentType && contentType.includes('application/octet-stream')) {
    data = await response.blob();
  } else {
    data = await response.text();
  }

  return { data, status: response.status, headers: response.headers };
};

// A Cloud18 session left unverifiable by a restart of replication-manager is resumed once (#2341):
// the server asks the Cloud18 realm whether the session's login still holds and gives a new
// session, or refuses (the person then signs in again). One resume at a time for all the requests
// refused together, and never twice for the same old session.
const resumeInFlight = new Map() // old session -> its resume
const resumeTried = new Set()
const resumeSession = (oldToken) => {
  if (!oldToken) return Promise.resolve(null)
  if (resumeInFlight.has(oldToken)) return resumeInFlight.get(oldToken)
  if (resumeTried.has(oldToken)) return Promise.resolve(null)
  resumeTried.add(oldToken)
  {
    const call = () => fetch('/api/auth/cloud18/resume', { method: 'POST', headers: { Authorization: `Bearer ${oldToken}`, Accept: '*/*' } })
    // 429: many people resuming at once behind one address after a restart; once more, a little later
    const p = call()
      .then((r) => (r.status === 429 ? new Promise((resolve) => setTimeout(resolve, 2000 + Math.random() * 3000)).then(call) : r))
      .then((r) => (r.ok ? r.json() : null))
      .then((body) => {
        const token = body?.token || null
        // only when the session refused is still the stored one (not a sign-in meanwhile)
        if (token && localStorage.getItem('user_token') === oldToken) {
          localStorage.setItem('user_token', token)
        }
        return token
      })
      .catch(() => null)
      .finally(() => { resumeInFlight.delete(oldToken) })
    resumeInFlight.set(oldToken, p)
    return p
  }
}

const performRequest = async (method, apiUrl, params, authValue, baseUrl = '') => {
  let headerURL = baseUrl;
  if (apiUrl === "login") {
    headerURL = '';
  }

  let url = resolveUrl(apiUrl, authValue, baseUrl);

  // If GET, convert params to query string
  if (method.toUpperCase() === 'GET' && params && typeof params === 'object') {
    const query = buildQueryString(params);
    if (query) {
      const fullUrl = `${url}?${query}`;

      if (fullUrl.length > 2000) {
        console.warn('GET request URL exceeds 2000 characters. This may not work on all browsers or servers.');
        // Optional: throw new Error('GET request URL too long. Use POST instead.');
      }

      url = fullUrl;
    }
    params = null; // GET should not have a body
  }

  const headers = {
    ...buildHeaders(authValue, params instanceof FormData ? '' : 'json', headerURL),
  };

  if (params instanceof FormData) {
    delete headers['Content-Type'];
  }

  const options = {
    method,
    headers,
    credentials: 'include',
    ...(params ? { body: params instanceof FormData ? params : JSON.stringify(params) } : {})
  };

  try {
    const response = await fetch(url, options);

    // The admin zone (#2262): a local call refused for want of the local admin is sent again with
    // the admin mode's token, asked for once (adminMode.js); cancelled, the refusal stands.
    if (response.status === 403 && authValue === 'local' && baseUrl === '' && response.headers.get(ADMIN_REQUIRED_HEADER)) {
      let adminToken = null
      try {
        adminToken = await adminMode.request()
      } catch (e) {
        return handleResponse(response)
      }
      const retried = await fetch(url, { ...options, headers: { ...headers, Authorization: `Bearer ${adminToken}` } })
      if (retried.status === 401) {
        adminMode.leave() // the admin session ended: the next refusal asks again
      }
      return handleResponse(retried)
    }

    // Only invalidate the session when the request actually carried an auth token.
    // Public 401s (login form, autologin, unauthenticated endpoints) must not
    // clear a valid concurrent session.
    if (response.status === 401 && baseUrl === '' && headers.Authorization) {
      // If a newer token was stored between request dispatch and response arrival,
      // this 401 came from a stale request — don't evict the fresh token.
      const tokenUsed = headers.Authorization.slice('Bearer '.length)
      const currentToken = localStorage.getItem('user_token')
      if (tokenUsed && currentToken && tokenUsed !== currentToken) {
        return handleResponse(response)
      }
      // a restart of replication-manager: the Cloud18 session may come back (#2341)
      const resumed = await resumeSession(tokenUsed)
      if (resumed) {
        const retried = await fetch(url, { ...options, headers: { ...headers, Authorization: `Bearer ${resumed}` } })
        if (retried.status !== 401) {
          return handleResponse(retried)
        }
      }
      clearLocalStorageByPrefix('user_token');
      localStorage.removeItem('username');
      window.dispatchEvent(new CustomEvent('repman:auth401'));
    }

    return handleResponse(response);
  } catch (error) {
    console.error(`${method} Request Error:`, error);
    throw error;
  }
};


const requestWrapper = (authValue, baseUrl = '') => ({
  get: (apiUrl, params) => performRequest('GET', apiUrl, params, authValue, baseUrl),
  post: (apiUrl, params) => performRequest('POST', apiUrl, params, authValue, baseUrl),
  put: (apiUrl, params) => performRequest('PUT', apiUrl, params, authValue, baseUrl),
  delete: (apiUrl, params) => performRequest('DELETE', apiUrl, params, authValue, baseUrl),
  getAll: (urls, params) => {
    const requests = urls.map((url) => {
      const resolvedUrl = resolveUrl(url, authValue, baseUrl);
      const headers = buildHeaders(authValue, 'json', baseUrl);

      const options = {
        method: 'GET',
        headers,
        ...(params ? { body: JSON.stringify(params) } : {})
      };

      return fetch(resolvedUrl, options);
    });

    return Promise.allSettled(requests).then((results) =>
      results.map((result, idx) =>
        result.status === 'fulfilled'
          ? { url: urls[idx], data: result.value }
          : { url: urls[idx], error: result.reason }
      )
    );
  }
});

export const localApi = requestWrapper('local'); // Wrapper for local API calls
export const publicApi = requestWrapper('public'); // Wrapper for local public API calls (no auth)
export const peerApi = (baseUrl) => requestWrapper('peer', baseUrl); // Wrapper for peer API calls
export const meetApi = requestWrapper('meet'); // Wrapper for Mattermost (meetAPI) calls

export const getApi = (baseURL = '') => {
  return baseURL ? peerApi(baseURL) : localApi;
};

// Utility function to clear localStorage keys with a specific prefix
export const clearLocalStorageByPrefix = (prefix) => {
  for (let i = localStorage.length - 1; i >= 0; i--) {
    const key = localStorage.key(i);
    if (key && key.startsWith(prefix)) {
      localStorage.removeItem(key);
    }
  }
}

export const getTokenByBaseURL = (baseURL) => {
  if (baseURL == "") {
    return localStorage.getItem(`user_token`);
  } else {
    return localStorage.getItem(`user_token_${baseURL}`);
  }
}


function buildQueryString(obj, parentKey = '') {
  const query = [];

  for (const key in obj) {
    if (!Object.hasOwn(obj, key)) continue;

    const value = obj[key];
    const fullKey = parentKey ? `${parentKey}_${key}` : key;

    if (value === '' || value === null || value === undefined) continue;

    if (Array.isArray(value)) {
      value.forEach(item => {
        query.push(`${encodeURIComponent(fullKey)}=${encodeURIComponent(item)}`);
      });
    } else if (typeof value === 'object') {
      query.push(buildQueryString(value, fullKey));
    } else {
      query.push(`${encodeURIComponent(fullKey)}=${encodeURIComponent(value)}`);
    }
  }

  return query.join('&');
}
