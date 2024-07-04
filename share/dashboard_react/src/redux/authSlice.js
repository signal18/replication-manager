import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
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

export const gitLogin = createAsyncThunk('auth/gitLogin', async ({}, thunkAPI) => {
  try {
    const response = await authService.gitLogin()
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
  initialState: { user: null, loading: false, error: null, isLogged: false },
  reducers: {
    logout: (state, action) => {
      localStorage.removeItem('user_token')
      state.user = null
      state.isLogged = false
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(login.pending, (state) => {
        state.loading = true
      })
      .addCase(login.fulfilled, (state, action) => {
        const { payload, meta } = action
        const { token } = payload
        const { arg } = meta
        localStorage.setItem('user_token', token)
        state.loading = false
        state.isLogged = true
        state.user = {
          username: arg.username
        }
      })
      .addCase(login.rejected, (state, action) => {
        state.loading = false
        state.error = action.payload.errorMessage
      })
      .addCase(gitLogin.pending, (state) => {
        state.loading = true
      })
      .addCase(gitLogin.fulfilled, (state, action) => {
        const { apiOAuthProvider, apiOAuthClientID, apiOAuthSecretID } = action.payload.config
        const authURL = `${apiOAuthProvider}/oauth/authorize?authority=${apiOAuthProvider}&client_id=${apiOAuthClientID}&secret_id=${apiOAuthSecretID}&redirect_uri=https://${location.host}:${location.port}/api/auth/callback&response_type=code&scope=openid profile email api`
        // Redirect the user to the oAuth URL
        window.location.href = authURL
      })
      .addCase(gitLogin.rejected, (state, action) => {
        state.loading = false
        state.error = action.payload.errorMessage
      })
  }
})

// this is for dispatch
export const { logout } = authSlice.actions

// this is for configureStore
export default authSlice.reducer
