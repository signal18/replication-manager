const toBase64 = (str) => btoa(unescape(encodeURIComponent(str)));

const authConfig = {
  1: { // Local calls
    resolveUrl: (apiUrl) => `/api/${apiUrl}`,
    getToken: () => localStorage.getItem('user_token')
  },
  2: { // Peer calls
    resolveUrl: (apiUrl, baseUrl) => `/peer/${baseUrl}/api/${apiUrl}`,
    getToken: (baseUrl) => {
      if (baseUrl == "") {
        return localStorage.getItem(`user_token`);
      } else {
        return localStorage.getItem(`user_token_${baseUrl}`);
      }
    }
  },
  3: { // Mattermost API
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
  const userID = localStorage.getItem('userID');

  return {
    ...getContentType(contentType),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(userID ? { 'X-User-ID': userID } : {}),
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

  return { data, status: response.status };
};

const performRequest = async (method, apiUrl, params, authValue, baseUrl = '') => {
  let headerURL = baseUrl
  if (apiUrl == "login") {
    headerURL = ''
  }
  const url = resolveUrl(apiUrl, authValue, baseUrl);
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
    if (response.status === 401 && baseUrl === '') {
      clearLocalStorageByPrefix('user_token')
      localStorage.removeItem('username')
      window.location.reload()
    } else {
      return handleResponse(response);
    }
  } catch (error) {
    console.error(`${method} Request Error:`, error);
    throw error;
  }
};

const requestWrapper = (authValue, baseUrl = '') => ({
  get: (apiUrl, params) => performRequest('GET', apiUrl, params, authValue, baseUrl),
  post: (apiUrl, params) => performRequest('POST', apiUrl, params, authValue, baseUrl),
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

export const localApi = requestWrapper(1); // Wrapper for local API calls
export const peerApi = (baseUrl) => requestWrapper(2, baseUrl); // Wrapper for peer API calls
export const meetApi = requestWrapper(3); // Wrapper for Mattermost (meetAPI) calls

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
