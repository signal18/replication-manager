import { getApi } from './apiHelper'

export const billingService = {
  getPersonalBalance,
  getSubscription,
  getSubscriptionPlans,
  changeSubscriptionPlan,
  getTransactions
}

function getPersonalBalance(baseURL = '') {
  return getApi(baseURL).get('billing/personal')
}

function getSubscription(baseURL = '') {
  return getApi(baseURL).get('billing/subscription')
}

function getSubscriptionPlans(baseURL = '') {
  return getApi(baseURL).get('billing/subscription/plans')
}

function changeSubscriptionPlan(subscription, baseURL = '') {
  return getApi(baseURL).post('billing/subscription/change', { subscription })
}

function getTransactions({ limit = 20, offset = 0, direction = 'desc' } = {}, baseURL = '') {
  return getApi(baseURL).get('billing/transactions', { limit, offset, direction })
}
