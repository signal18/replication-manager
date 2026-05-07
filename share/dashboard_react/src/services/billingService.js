import { getApi } from './apiHelper'

export const billingService = {
  getPersonalBalance,
  getSubscription,
  getTransactions
}

function getPersonalBalance(baseURL = '') {
  return getApi(baseURL).get('billing/personal')
}

function getSubscription(baseURL = '') {
  return getApi(baseURL).get('billing/subscription')
}

function getTransactions({ limit = 20, offset = 0, direction = 'desc' } = {}, baseURL = '') {
  return getApi(baseURL).get('billing/transactions', { limit, offset, direction })
}
