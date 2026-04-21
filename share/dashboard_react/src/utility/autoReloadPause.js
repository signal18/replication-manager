export const AUTO_RELOAD_PAUSE_KEY = 'pause_auto_reload'

const AUTO_RELOAD_PAUSE_LOCK_COUNT_KEY = 'pause_auto_reload_lock_count'
const AUTO_RELOAD_PAUSE_PREV_VALUE_KEY = 'pause_auto_reload_prev_value'
const NO_PREV_VALUE = '__none__'

const parseCount = (rawValue) => {
  const parsed = parseInt(rawValue || '0', 10)
  return Number.isNaN(parsed) ? 0 : parsed
}

export const acquireAutoReloadPause = () => {
  const currentCount = parseCount(localStorage.getItem(AUTO_RELOAD_PAUSE_LOCK_COUNT_KEY))

  if (currentCount === 0) {
    const previousPauseState = localStorage.getItem(AUTO_RELOAD_PAUSE_KEY)
    localStorage.setItem(AUTO_RELOAD_PAUSE_PREV_VALUE_KEY, previousPauseState ?? NO_PREV_VALUE)
    localStorage.setItem(AUTO_RELOAD_PAUSE_KEY, 'true')
  }

  localStorage.setItem(AUTO_RELOAD_PAUSE_LOCK_COUNT_KEY, String(currentCount + 1))
}

export const releaseAutoReloadPause = () => {
  const currentCount = parseCount(localStorage.getItem(AUTO_RELOAD_PAUSE_LOCK_COUNT_KEY))
  if (currentCount <= 0) {
    return
  }

  const nextCount = currentCount - 1
  if (nextCount > 0) {
    localStorage.setItem(AUTO_RELOAD_PAUSE_LOCK_COUNT_KEY, String(nextCount))
    return
  }

  localStorage.removeItem(AUTO_RELOAD_PAUSE_LOCK_COUNT_KEY)

  const previousPauseState = localStorage.getItem(AUTO_RELOAD_PAUSE_PREV_VALUE_KEY)
  localStorage.removeItem(AUTO_RELOAD_PAUSE_PREV_VALUE_KEY)

  if (previousPauseState && previousPauseState !== NO_PREV_VALUE) {
    localStorage.setItem(AUTO_RELOAD_PAUSE_KEY, previousPauseState)
  } else {
    localStorage.removeItem(AUTO_RELOAD_PAUSE_KEY)
  }
}
