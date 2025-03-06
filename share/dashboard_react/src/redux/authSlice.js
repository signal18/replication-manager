import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { authService } from '../services/authService'
import { clearLocalStorageByPrefix } from '../services/apiHelper'

export const login = createAsyncThunk('auth/login', async ({ username, password }, thunkAPI) => {
  try {
    const response = await authService.login(username, password, '')
    console.log('response::', response)
    return response
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

export const authSlice = createSlice({
  name: 'auth',
  initialState: { 
    user: null, 
    loading: false, 
    loadingGitLogin: false, 
    loadingPeerLogin: false, 
    error: null, 
    isLogged: false, 
    isPeerLogged: false, 
    baseURL: '',
  },
  reducers: {
    logout: (state, action) => {
      clearLocalStorageByPrefix('user_token')
      localStorage.removeItem('username')
      state.user = null
      state.isLogged = false
    },
    setUserData: (state, action) => {
      const username = localStorage.getItem('username')
      state.user = {
        username: username
      }
    },
    setBaseURL: (state, action) => {
      state.baseURL = action.payload.baseURL
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
      localStorage.setItem('userID', typeof data === 'string' ? JSON.parse(data)?.user_id : data?.user_id)
      localStorage.setItem('user_token', typeof data === 'string' ? JSON.parse(data)?.token : data?.token)
      localStorage.setItem('username', arg.username)
      state.isLogged = true
      state.user = {
        username: arg.username
      }
      if (action.type === 'login') {
        state.loading = false
      }
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
  }
})

// this is for dispatch
export const { logout, setUserData, setBaseURL } = authSlice.actions

// this is for configureStore
export default authSlice.reducer
