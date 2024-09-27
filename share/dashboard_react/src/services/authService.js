import { postRequest } from './apiHelper'

export const authService = {
  login,
  gitLogin
}

function login(username, password) {
  return postRequest('login', { username, password }, 0)
}

function gitLogin(username, password) {
  return postRequest('login-git', { username, password }, 0)
}
