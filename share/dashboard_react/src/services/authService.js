import { getApi, localApi, publicApi } from './apiHelper'

export const authService = {
  login,
  gitLogin,
  signup,
  whoami,
  getSignupPromo,
  getSignupStatus,
  pollUpgrade,
}

function login(username, password, baseURL) {
  if (baseURL) {
    return getApi(baseURL).post('login', { username, password })
  }
  return publicApi.post('login', { username, password })
}

function gitLogin(username, password, baseURL) {
  return getApi(baseURL).post('login-git', { username, password })
}

function whoami(baseURL) {
  return getApi(baseURL).get('whoami')
}

function signup(payload, baseURL) {
  if (baseURL) {
    return getApi(baseURL).post('signup', payload)
  }
  return publicApi.post('signup', payload)
}

function getSignupPromo() {
  return publicApi.get('signup/promo')
}

function getSignupStatus(email) {
  return publicApi.get('signup/status', { email })
}

function pollUpgrade(upgradeId) {
  return localApi.get('login/upgrade', { upgrade_id: upgradeId })
}
