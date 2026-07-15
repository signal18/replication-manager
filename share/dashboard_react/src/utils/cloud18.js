// Plans eligible for paid Cloud18 features (external arbitration, dynamic
// cluster git import); must mirror config.Config.IsEligibleForArbitration()
// on the server side.
export const CLOUD18_PAID_PLANS = ['support', 'support-services', 'partner']

export const isCloud18PlanEligible = (config) => {
  const plan = (config?.cloud18SubscriptionPlan || '').trim().toLowerCase()
  return !!config?.cloud18GitUser && CLOUD18_PAID_PLANS.includes(plan)
}
