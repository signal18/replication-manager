// Admin mode (#2262): the admin zone (global settings, the host terminal) is changed by the local
// admin account only. A person signed in another way (Cloud18) enters admin mode by typing the local
// admin user and password: the GUI logs in with them (POST /api/login) and keeps that session's token
// IN MEMORY ONLY (never localStorage: a reload or another tab asks again), until it ends or the
// person leaves admin mode. Only a request the server refused for want of the local admin
// (X-Repman-Admin-Required) is sent again with it; every other call keeps the person's own session.

export const ADMIN_REQUIRED_HEADER = 'X-Repman-Admin-Required'
const CHANGED = 'repman:admin-changed'
const REQUIRED = 'repman:admin-required'

let adminToken = null
let adminUser = ''
let expiresAt = 0
let pending = null // the open request for the admin login: one prompt for concurrent refusals
let expiryTimer = null

// tokenEnd is the end of a JWT (its exp claim), 0 when unreadable.
const tokenEnd = (token) => {
  try {
    const payload = JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')))
    return payload.exp ? payload.exp * 1000 : 0
  } catch (e) {
    return 0
  }
}

const changed = () => window.dispatchEvent(new CustomEvent(CHANGED))

export const adminMode = {
  CHANGED,
  REQUIRED,
  isActive: () => adminToken !== null && Date.now() < expiresAt,
  token: () => (adminMode.isActive() ? adminToken : null),
  user: () => adminUser,
  enter: (token, user) => {
    adminToken = token
    adminUser = user
    expiresAt = tokenEnd(token) || Date.now() + 10 * 60 * 1000
    clearTimeout(expiryTimer)
    expiryTimer = setTimeout(adminMode.leave, Math.max(0, expiresAt - Date.now()))
    if (pending) {
      pending.resolve(token)
      pending = null
    }
    changed()
  },
  leave: () => {
    adminToken = null
    adminUser = ''
    expiresAt = 0
    clearTimeout(expiryTimer)
    changed()
  },
  // request asks the person for the local admin login (the AdminModeModal answers it) and
  // resolves with the admin token, or rejects when they cancel.
  request: () => {
    if (adminMode.isActive()) return Promise.resolve(adminToken)
    if (!pending) {
      let resolve, reject
      const promise = new Promise((res, rej) => {
        resolve = res
        reject = rej
      })
      pending = { promise, resolve, reject }
      window.dispatchEvent(new CustomEvent(REQUIRED))
    }
    return pending.promise
  },
  cancel: () => {
    if (pending) {
      pending.reject(new Error('admin mode cancelled'))
      pending = null
    }
  }
}
