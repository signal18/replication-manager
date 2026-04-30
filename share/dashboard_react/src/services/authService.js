import { getApi } from './apiHelper'

export const authService = {
  login,
  gitLogin,
  signup,
  whoami
}

function login(username, password, baseURL) {
  return getApi(baseURL).post('login', { username, password })
}

function gitLogin(username, password, baseURL) {
  return getApi(baseURL).post('login-git', { username, password })
}

function whoami(baseURL) {
  return getApi(baseURL).get('whoami')
}

function signup(payload, baseURL) {
  return getApi(baseURL).post('signup', payload)
}
