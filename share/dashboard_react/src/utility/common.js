import { showErrorToast, showSuccessToast } from '../redux/toastSlice'

export const isAuthorized = () => {
  return localStorage.getItem('user_token') !== null
}

export const getRefreshInterval = () => {
  return localStorage.getItem('refresh_interval')
}

export const gtidstring = function (arr) {
  let output = []
  if (arr?.length > 0) {
    output = arr.map((item) => {
      return item.domainId + '-' + item.serverId + '-' + item.seqNo
    })
    return output.join(', ')
  }
  return ''
}

export const showSuccessBanner = (message, responseStatus, thunkAPI) => {
  thunkAPI.dispatch(
    showSuccessToast({
      status: 'success',
      title: message
    })
  )
}
export const showErrorBanner = (message, error, thunkAPI) => {
  thunkAPI.dispatch(
    showErrorToast({
      status: 'error',
      title: message,
      description: error
    })
  )
}

export const handleError = (error, thunkAPI) => {
  const errorMessage = error.message || 'Request failed'
  const errorStatus = error.errorStatus || 500 // Default error status if not provided
  // Handle errors (including custom errorStatus)
  return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
}

export const convertObjectToArray = (inputObject) => {
  return Object.keys(inputObject).map((key) => {
    return { name: key, value: key }
  })
}
