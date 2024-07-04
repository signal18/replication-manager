import { postRequest } from './apiHelper'

export const authService = {
  login,
  gitLogin
}

function login(username, password) {
  return postRequest('login', { username, password }, 0)
}

function gitLogin() {
    console.log('inside gitLogin')
  return postRequest('monitor', {}, 0)
}
