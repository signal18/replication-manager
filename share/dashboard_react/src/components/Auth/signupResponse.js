export const pendingSignupMessage = 'Waiting for email confirmation. Check your email to confirm your account.'
export const existingAccountMessage = 'Account already exists. Please log in.'

export const isPendingSignupResponse = (response) => {
  const data = response?.data || {}
  return response?.status === 202 || data?.state === 'pending' || data?.identity?.email_confirmed === false
}

export const isConfirmedSignupResponse = (response) => {
  const data = response?.data || {}
  return response?.status === 201 && data?.state !== 'pending' && data?.identity?.email_confirmed !== false
}

export const shouldRedirectToLoginAfterSignup = (response) => isConfirmedSignupResponse(response)

export const resolveSignupSuccessMessage = (response, fallbackMessage) => {
  if (isPendingSignupResponse(response)) {
    return pendingSignupMessage
  }
  if (typeof fallbackMessage === 'function') {
    return fallbackMessage(response)
  }
  return fallbackMessage
}

export const resolveSignupErrorMessage = (response) => {
  if (response?.status === 409) {
    return existingAccountMessage
  }
  if (typeof response?.data === 'object' && response.data?.message) {
    return response.data.message
  }
  if (typeof response?.data === 'object' && response.data?.error) {
    return response.data.error
  }
  if (typeof response?.data === 'string' && response.data) {
    return response.data
  }
  return 'Signup failed'
}
