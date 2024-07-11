export const isAuthorized = () => {
  return localStorage.getItem('user_token') !== null
}

export const getRefreshInterval = () => {
  return localStorage.getItem('refresh_interval')
}
