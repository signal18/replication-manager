import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { authService } from '../services/authService'
import { clearLocalStorageByPrefix } from '../services/apiHelper'

export const login = createAsyncThunk('auth/login', async ({ username, password }, thunkAPI) => {
  try {
    const response = await authService.login(username, password, '')
    if (response.status == 200){
      return response
    } else {
      return thunkAPI.rejectWithValue({ errorMessage: response.data || "Request failed", errorStatus: response.status || 500 })
    }
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const peerLogin = createAsyncThunk('auth/peerLogin', async ({  password, baseURL }, thunkAPI) => {
  try {
    const response = await authService.login(thunkAPI.getState().auth.user.username, password, baseURL)
    if (response.status == 200){
      setBaseURL({baseURL})
    }
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const whoami = createAsyncThunk('auth/whoami', async (_, thunkAPI) => {
  const tokenAtDispatch = localStorage.getItem('user_token')
  try {
    const response = await authService.whoami(thunkAPI.getState().auth.baseURL)
    if (response.status == 200){
      return response
    } else {
      return thunkAPI.rejectWithValue({ errorMessage: response.data || "Request failed", errorStatus: response.status || 500, tokenAtDispatch })
    }
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus, tokenAtDispatch }) // Pass the entire Error object to the rejected action
  }
}, 
// Add a condition to prevent the action from being dispatched if the user is already fetching the info
{
  condition: (_, { getState }) => {
    const { auth } = getState();
    if (auth.isLoadingUserData) {
      return false;
    }
  }
});

export const authSlice = createSlice({
  name: 'auth',
  initialState: {
    user: null,
    loading: false,
    loadingGitLogin: false,
    loadingPeerLogin: false,
    isLoadingUserData: false,
    error: null,
    isLogged: false,
    isPeerLogged: false,
    baseURL: '',
    // SSO async upgrade state. ssoUpgradeId is set when login returns an upgrade_id.
    // SSOUpgradePoller reads this and replaces user_token once the upgrade completes.
    ssoUpgradeId: null,
    // sessionStatus tracks whoami validation result:
    //   'unknown'         — not yet checked (initial / post-mount)
    //   'authenticated'   — whoami confirmed valid session
    //   'unauthenticated' — 401: session invalid, token cleared
    //   'unavailable'     — 5xx/network: server unreachable, keep existing session
    sessionStatus: 'unknown',
    // Incremented each time whoami rejects with a non-401 error.
    // SessionGuard watches this to schedule the next retry without recursive timers.
    unavailableRetryCount: 0,
  },
  reducers: {
    logout: (state) => {
      clearLocalStorageByPrefix('user_token')
      localStorage.removeItem('username')
      sessionStorage.removeItem('meet_unavailable')
      state.user = null
      state.isLogged = false
      state.ssoUpgradeId = null
      state.sessionStatus = 'unauthenticated'
    },
    setUserData: (state) => {
      const username = localStorage.getItem('username')
      state.isLogged = localStorage.getItem('user_token') ? true : false
      state.user = state.user ? {
        ...state.user,
        username: username
      } : {
        username: username
      }
      // Token came directly from the server (autologin / dashboard-token), treat as authenticated.
      if (state.isLogged) state.sessionStatus = 'authenticated'
    },
    setBaseURL: (state, action) => {
      state.baseURL = action.payload.baseURL
    },
    clearSSOUpgrade: (state) => {
      state.ssoUpgradeId = null
    },
  },
  extraReducers: (builder) => {
    builder.addMatcher(isAnyOf(login.pending, peerLogin.pending), (state, action) => {
      if (action.type === 'login') {
        state.loading = true
      } else if (action.type === 'peerLogin') {
        state.loadingPeerLogin = true
      }
    })
    builder.addMatcher(isAnyOf(login.fulfilled), (state, action) => {
      const { payload, meta } = action
      const { data } = payload
      const { arg } = meta
      const parsed = typeof data === 'string' ? JSON.parse(data) : data
      localStorage.setItem('user_token', parsed?.token)
      localStorage.setItem('username', arg.username)
      state.isLogged = true
      state.user = { username: arg.username }
      state.sessionStatus = 'authenticated'
      if (action.type === 'login') {
        state.loading = false
      }
      // Always reset ssoUpgradeId so a previous stale id can never persist
      // into a fresh login session (regardless of whether this login returned
      // an upgrade_id or not).
      state.ssoUpgradeId = parsed?.upgrade_id ?? null
    })
    builder.addMatcher(isAnyOf(peerLogin.fulfilled), (state, action) => {
      const { payload, meta } = action
      const { data } = payload
      const { arg } = meta

      state.baseURL = arg.baseURL
      const encodedBaseUrl = btoa(arg.baseURL)
      if (action.payload.status === 200) {
        localStorage.setItem(`user_token_${encodedBaseUrl}`, typeof data === 'string' ? JSON.parse(data)?.token : data?.token)
        state.isPeerLogged = true 
      } else {
        localStorage.removeItem(`user_token_${encodedBaseUrl}`)
      }
      state.loadingPeerLogin = false
    })
    builder.addMatcher(isAnyOf(login.rejected,peerLogin.rejected), (state, action) => {
      if (action.type === 'login') {
        state.loading = false
      } else if (action.type === 'peerLogin') {
        state.loadingPeerLogin = false
        state.isPeerLogged = false 
      } 
      state.error = action?.payload?.errorMessage
    })
    builder.addMatcher(isAnyOf(whoami.pending), (state) => {
      state.isLoadingUserData = true
    })
    builder.addMatcher(isAnyOf(whoami.rejected), (state, action) => {
      state.isLoadingUserData = false
      const status = action.payload?.errorStatus
      if (status === 401) {
        // If a newer token was stored after this whoami was dispatched, the 401
        // came from a stale request and the fresh token should not be evicted.
        const currentToken = localStorage.getItem('user_token')
        const usedToken = action.payload?.tokenAtDispatch
        if (usedToken && currentToken && usedToken !== currentToken) {
          return
        }
        // Invalid session — clear credentials and force re-login.
        clearLocalStorageByPrefix('user_token')
        localStorage.removeItem('username')
        state.user = null
        state.isLogged = false
        state.ssoUpgradeId = null
        state.sessionStatus = 'unauthenticated'
      } else {
        // 5xx / network error during restart — server is temporarily unreachable.
        // Keep existing credentials; let the app stay mounted and retry later.
        state.sessionStatus = 'unavailable'
        state.unavailableRetryCount += 1
      }
    })
    builder.addMatcher(isAnyOf(whoami.fulfilled), (state, action) => {
      const { payload } = action
      const { data } = payload
      const username = localStorage.getItem('username')
      state.user = {
        ...state.user,
        ...data,
        username: username
      }
      state.isLogged = true
      state.isLoadingUserData = false
      state.sessionStatus = 'authenticated'
      state.unavailableRetryCount = 0
    })
  }
})

// this is for dispatch
export const { logout, setUserData, setBaseURL, clearSSOUpgrade } = authSlice.actions

// this is for configureStore
export default authSlice.reducer
