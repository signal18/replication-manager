import { getRequest, postRequest } from './apiHelper'

export const meetService = {
  getMeet
}

function getMeet() {
  return getRequest('meet', null, 1, true)
}
