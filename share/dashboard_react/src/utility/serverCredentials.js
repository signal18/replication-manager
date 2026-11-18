// holdsServerCredentials: the logged user is the local admin, or holds the server credentials
// grant on every cluster it has grants on (the instance's sysops and extsysops): it may rotate the
// admin password from the user pill.
export function holdsServerCredentials(loggedUser) {
  if (loggedUser?.User === 'admin') return true
  const grants = loggedUser?.grants || {}
  const clusters = Object.keys(grants)
  return clusters.length > 0 && clusters.every((c) => grants[c]?.['cluster-server-credentials'])
}
