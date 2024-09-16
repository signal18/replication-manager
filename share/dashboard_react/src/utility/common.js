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

export const convertObjectToArrayForDropdown = (inputObject) => {
  if (Array.isArray(inputObject)) {
    return inputObject.map((obj) => ({ name: obj, value: obj }))
  }
  return Object.keys(inputObject).map((key) => {
    return { name: key, value: key }
  })
}

export const convertObjectToArray = (inputObject) => {
  return Object.keys(inputObject).map((key) => {
    return inputObject[key]
  })
}

export const getDaysInMonth = (month, year = new Date().getFullYear()) => {
  // Create a date object with the next month and the first day
  const date = new Date(year, month - 1, 1)

  // Set the date object to the last day of the previous month
  date.setMonth(date.getMonth() + 1)
  date.setDate(0)
  // Get the number of days in the month
  const daysInMonth = date.getDate()

  // Create an array with the days of the month
  return Array.from({ length: daysInMonth }, (_, i) => {
    return { name: i + 1, value: i + 1 }
  })
}

export const padWithZero = (number) => {
  const res = number < 10 ? `0${number}` : number !== 'undefined' ? `${number}` : ''

  return res
}

export const formatBytes = (bytes, decimals = 2) => {
  if (bytes === 0) return '0 Bytes'

  const k = 1024
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`
}

export const compareTimes = (startTime, endTime) => {
  // Assuming the times are in the format "HH:MM:SS"
  const today = new Date().toISOString().split('T')[0] // Get today's date in "YYYY-MM-DD" format

  const startDate = new Date(`${today}T${startTime}`)
  const endDate = new Date(`${today}T${endTime}`)

  if (endDate <= startDate) {
    return false
  }

  return true // Times are valid
}

export const getOrdinalSuffix = (n) => {
  const s = ['th', 'st', 'nd', 'rd']
  const v = n % 100
  return n + (s[(v - 20) % 10] || s[v] || s[0])
}

export const getBackupMethod = (methodId) => {
  switch (methodId) {
    case 1:
      return 'Logical'
    case 2:
      return 'Physical'
    default:
      return 'Unknown'
  }
}

export const getBackupStrategy = (strategyId) => {
  switch (strategyId) {
    case 1:
      return 'Full'
    case 2:
      return 'Incremental'
    case 3:
      return 'Differential'
    default:
      return 'Unknown'
  }
}

export const formatDate = (date, format) => {
  if (typeof date === 'string') {
    date = new Date(date)
  }
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0') // Months are zero-based
  const day = String(date.getDate()).padStart(2, '0')

  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  const seconds = String(date.getSeconds()).padStart(2, '0')
  // if (format === 'YYYY-MM-DD HH:MI:SS') {
  //   return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
  // }
  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
}

export const getReadableTime = (timeInseconds) => {
  if (timeInseconds < 60) {
    return `${timeInseconds} seconds`
  }
  const minutes = timeInseconds / 60
  if (minutes < 60) {
    return `${Math.round(minutes)} minutes`
  }
  const hours = minutes / 24
  if (hours < 24) {
    return `${Math.round(hours)} hours`
  }
  console.log()
  return `${Math.round(hours / 7)} days`
}

export const isEqualLongQueryTime = (a, b) => {
  if (Number(a) == Number(b)) {
    return true
  }
  return false
}
