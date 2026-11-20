// The colour of each domain's pill in the cluster header (Navbar), one place for every
// view that shows a domain's colour: the header pills and the count badges of the
// cluster's log tabs (Pages/Dashboard/components/ClusterLogsTabs). Each function returns a
// Chakra colour scheme, its severity first where the pill varies by the worst state.

// the security grade of the cluster's security score
export const GRADE_COLOR = { A: 'green', B: 'teal', C: 'yellow', D: 'orange', F: 'red' }

const states = (clusterData, key) => clusterData?.[key] || []
const hasErrType = (list, errType) => list.some((s) => s.ErrType === errType)

// HA (the Cluster health pill): red on a blocker, orange on a warning, green when clean
export function clusterHealthColor(blockers = 0, warnings = 0) {
  if (blockers > 0) return 'red'
  if (warnings > 0) return 'orange'
  return 'green'
}

// Security: the grade's colour while security states are open, gray otherwise
export function securityColor(clusterData) {
  if (states(clusterData, 'securityStates').length === 0) return 'gray'
  return GRADE_COLOR[clusterData?.securityScore?.grade] || 'orange'
}

// Workload: purple while workload states are open
export function workloadColor(clusterData) {
  return states(clusterData, 'workloadStates').length > 0 ? 'purple' : 'gray'
}

// Schema: red on an ERROR state, cyan while other schema states are open
export function schemaColor(clusterData) {
  const list = states(clusterData, 'schemaStates')
  if (hasErrType(list, 'ERROR')) return 'red'
  return list.length > 0 ? 'cyan' : 'gray'
}

// Config: red on an ERROR state, yellow on a WARNING, blue while other config states are open
export function configColor(clusterData) {
  const list = states(clusterData, 'configStates')
  if (hasErrType(list, 'ERROR')) return 'red'
  if (hasErrType(list, 'WARNING')) return 'yellow'
  return list.length > 0 ? 'blue' : 'gray'
}

// Jobs: the colour of the running-backup pill (jobs have no state machine of their own)
export const JOBS_COLOR = 'teal'
