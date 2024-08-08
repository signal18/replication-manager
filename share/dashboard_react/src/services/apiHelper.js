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

export async function getRequest(apiUrl, params, authValue = 1) {
  try {
    const response = await fetch(`/api/${apiUrl}`, {
      method: 'GET',
      headers: authHeader(authValue),
      ...(params ? { body: JSON.stringify(params) } : {})
    })
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
  } catch (error) {
    console.error('Error occured:', error)
    throw error
  }
}

export function getRequestAll(urls, params, authValue = 1) {
  const requestHeaders = {
    method: 'GET',
    headers: authHeader(authValue),
    ...(params ? { body: JSON.stringify(params) } : {})
  }
  const fetchUrls = urls.map((url) => fetch(`/api/${url}`, requestHeaders))
  return Promise.all(fetchUrls).then((responses) => responses)
}

export async function postRequest(apiUrl, params, authValue = 1) {
  try {
    const response = await fetch(`/api/${apiUrl}`, {
      method: 'POST',
      headers: authHeader(authValue), // Spread the headers from authHeader
      body: JSON.stringify(params)
    })

    // Handle HTTP errors
    const contentType = response.headers.get('Content-Type')
    let data = null
    if (contentType && contentType.includes('application/json')) {
      data = await response.json()
    } else if (contentType && contentType.includes('text/plain')) {
      // Handle plain text response
      const data = await response.text()
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
