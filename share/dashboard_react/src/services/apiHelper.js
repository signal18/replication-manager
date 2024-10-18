export default function authHeader(authValue = 1, contentType = 'json') {
  let headerObj = {
    ...getContentType(contentType),
    Accept: '*/*'
  }
  if (authValue === 1) {
    let accessToken = localStorage.getItem('user_token')
    headerObj = {
      ...headerObj,
      Authorization: `Bearer ${accessToken}`
    }
  }
  return headerObj
}

const getContentType = (type) => {
  if (type === 'json') {
    return { 'Content-Type': 'application/json; charset="utf-8"' }
  }
  return {}
}

export async function getRequest(apiUrl, params, authValue = 1, isMeetApi = false) {
  try {
    const response = await fetch(isMeetApi ? `/${apiUrl}` : `/api/${apiUrl}`, {
      method: 'GET',
      headers: authHeader(authValue),
      ...(params ? { body: JSON.stringify(params) } : {})
    })

    if (response.status === 401) {
      localStorage.removeItem('user_token')
      localStorage.removeItem('username')
      window.location.reload()
    } else {
      const contentType = response.headers.get('Content-Type')
      let data = null
      if (contentType && contentType.includes('application/json')) {
        data = await response.json()
      } else if (contentType && contentType.includes('text/plain')) {
        data = await response.text()
        try {
          data = JSON.parse(data)
        } catch (e) {
          throw new Error(data)
        }
      }
      return {
        data,
        status: response.status
      }
    }
  } catch (error) {
    console.error('Error occured:', error)
    throw error
  }
}

export function getRequestAll(urls, params, authValue = 1, isMeetApi = false) {
  const requestHeaders = {
    method: 'GET',
    headers: authHeader(authValue),
    ...(params ? { body: JSON.stringify(params) } : {})
  }
  const fetchUrls = urls.map((url) => fetch(isMeetApi ? `/${url}` : `/api/${url}`, requestHeaders))
  return Promise.all(fetchUrls).then((responses) => responses)
}

export async function postRequest(apiUrl, params, authValue = 1, isMeetApi = false) {
  try {
    const response = await fetch(isMeetApi ? `/${apiUrl}` : `/api/${apiUrl}`, {
      method: 'POST',
      headers: authHeader(authValue), // Spread the headers from authHeader
      body: JSON.stringify(params)
    })

    const contentType = response.headers.get('Content-Type')
    // Handle HTTP errors
    let data = null

    if (contentType && contentType.includes('application/json')) {
      data = await response.json()
      if (response.status === 403 || response.status === 401) {
        throw new Error(data)
      }
    } else if (contentType && contentType.includes('text/plain')) {
      // Handle plain text response
      data = await response.text()
      if (response.status === 403 || response.status === 401) {
        throw new Error(data)
      }
    }

    return {
      data,
      status: response.status
    }
  } catch (error) {
    // Handle other errors (e.g., network issues)
    console.error('Error:', error)
    throw error
  }
}
