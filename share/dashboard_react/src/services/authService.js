import { getApi, localApi, publicApi } from './apiHelper'

export const authService = {
  login,
  gitLogin,
  signup,
  whoami,
  getSignupPromo,
  getSignupStatus,
  pollUpgrade,
  cloud18SSOStatus,
  cloud18SSOSession,
  cloud18SSOLogoutEnd,
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

// Sign in with Cloud18 (#2185): offered on a partner repman with a public address.
function cloud18SSOStatus() {
  return publicApi.get('auth/cloud18')
}

// Trades the one-time code the Cloud18 callback put in the address for the session, once.
function cloud18SSOSession(code) {
  return publicApi.post('auth/cloud18/session', { code })
}

// Ends the realm session of this Cloud18 session (its token, read before the dashboard drops it) and
// answers where the browser goes: the realm's logout then asks nothing (#2292).
function cloud18SSOLogoutEnd() {
  return localApi.post('auth/cloud18/logout')
}

function pollUpgrade(upgradeId) {
  return localApi.get('login/upgrade', { upgrade_id: upgradeId })
}
