const DEFAULT_RID = 'container#db'
const JOBS_RID = 'container#jobs'

export const OpenSVCTerminalRID = {
  Default: DEFAULT_RID,
  Jobs: JOBS_RID,
}

export const resolveServiceContainerFromRID = (rid) => {
  return rid === JOBS_RID ? JOBS_RID : DEFAULT_RID
}

// Keep rid query parameter only when it carries a meaningful non-default
// selection for OpenSVC server shell terminals.
export const normalizeRIDSearchParams = (searchParams, isOpenSVCServerShellTerminal) => {
  const nextParams = new URLSearchParams(searchParams)
  const rid = nextParams.get('rid')

  if (!isOpenSVCServerShellTerminal) {
    if (rid !== null) {
      nextParams.delete('rid')
      return { params: nextParams, changed: true }
    }
    return { params: nextParams, changed: false }
  }

  if (rid === null || rid === JOBS_RID) {
    return { params: nextParams, changed: false }
  }

  // Normalize unsupported/default values back to canonical default behavior
  // represented by absence of rid query parameter.
  nextParams.delete('rid')
  return { params: nextParams, changed: true }
}

export const setRIDSearchParam = (searchParams, serviceContainer) => {
  const nextParams = new URLSearchParams(searchParams)
  if (serviceContainer === JOBS_RID) {
    nextParams.set('rid', JOBS_RID)
  } else {
    nextParams.delete('rid')
  }
  return nextParams
}
