// The window shown after a password rotation (the Access tab and the user pill): where the new
// password went (mailed as a one-time link, or shown once here) and what is not done yet.
export function rotationTitle(r) {
  return (r.revealMailedTo
    ? 'Password of ' + r.username + ' rotated: the new one was mailed to ' + r.revealMailedTo + ' as a one-time link (until ' + new Date(r.revealExpires).toLocaleString() + '), it is not shown here'
    : 'New password of ' + r.username + ' (shown once)' + (r.revealError ? ' — ' + r.revealError : ''))
    + (r.tokensError ? ' — ' + r.tokensError : '')
    + (r.persistError ? ' — ' + r.persistError : '')
    + (r.etcFile && r.etcFile !== 'written' ? ' — configuration file: ' + r.etcFile : '')
    + (r.storesBehindCount ? ' — ' + r.storesBehindCount + ' server credential store(s) already behind, they may still hold the old password' + (r.storesBehind?.length ? ': ' + r.storesBehind.join(', ') : '') : '')
    + (r.scope === 'instance' ? ' — the server credential stores get the new list at their next pass' : '')
}

export function rotationText(r) {
  return r.revealMailedTo ? 'Mailed to ' + r.revealMailedTo : r.password
}

// rotationCopyable: there is a password to copy, it was shown here instead of being mailed
export function rotationCopyable(r) {
  return !r.revealMailedTo
}
