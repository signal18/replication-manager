import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { authService } from '../services/authService'

export const login = createAsyncThunk('auth/login', async ({ username, password }, thunkAPI) => {
  try {
    const response = await authService.login(username, password)
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const gitLogin = createAsyncThunk('auth/gitLogin', async ({ username, password }, thunkAPI) => {
  try {
    const response = await authService.gitLogin(username, password)
    console.log('response::', response)
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
  initialState: { user: null, loading: false, loadingGitLogin: false, error: null, isLogged: false },
  reducers: {
    logout: (state, action) => {
      localStorage.removeItem('user_token')
      localStorage.removeItem('username')
      state.user = null
      state.isLogged = false
    },
    setUserData: (state, action) => {
      const username = localStorage.getItem('username')
      state.user = {
        username: username
      }
    }
  },
  extraReducers: (builder) => {
    builder.addMatcher(isAnyOf(login.pending, gitLogin.pending), (state, action) => {
      if (action.type === 'login') {
        state.loading = true
      } else {
        state.loadingGitLogin = true
      }
    })
    builder.addMatcher(isAnyOf(login.fulfilled, gitLogin.fulfilled), (state, action) => {
      const { payload, meta } = action
      const { data } = payload
      const { arg } = meta

      localStorage.setItem('user_token', JSON.parse(data)?.token)
      localStorage.setItem('username', arg.username)
      state.isLogged = true
      state.user = {
        username: arg.username
      }
      if (action.type === 'login') {
        state.loading = false
      } else {
        state.loadingGitLogin = false
      }
    })
    builder.addMatcher(isAnyOf(login.rejected, gitLogin.rejected), (state, action) => {
      if (action.type === 'login') {
        state.loading = false
      } else {
        state.loadingGitLogin = false
      }
      state.error = action?.payload?.errorMessage
    })
  }
})

// this is for dispatch
export const { logout, setUserData } = authSlice.actions

// this is for configureStore
export default authSlice.reducer
