import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { clusterService } from '../services/clusterService'

export const getClusters = createAsyncThunk('cluster/getClusters', async ({}, thunkAPI) => {
  try {
    const response = await clusterService.getClusters()
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const clusterSlice = createSlice({
  name: 'cluster',
  initialState: { loading: false, error: null, cluster: null, refreshInterval: 0 },
  reducers: {
    setRefreshInterval: (state, action) => {
      localStorage.setItem('refresh_interval', action.payload.interval)
      state.refreshInterval = action.payload.interval
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(getClusters.pending, (state) => {
        state.loading = true
      })
      .addCase(getClusters.fulfilled, (state, action) => {
        state.loading = false
      })
      .addCase(getClusters.rejected, (state, action) => {
        state.loading = false
      })
  }
})

export const { setRefreshInterval } = clusterSlice.actions

// this is for configureStore
export default clusterSlice.reducer
