import { createAsyncThunk, createSlice } from '@reduxjs/toolkit'
import { billingService } from '../services/billingService'
import { extractApiErrorMessage } from '../utils/apiError'

const initialState = {
  balance: null,
  subscription: null,
  transactions: [],
  transactionsMeta: null,
  loadingBalance: false,
  loadingSubscription: false,
  loadingTransactions: false,
  errorBalance: null,
  errorSubscription: null,
  errorTransactions: null
}

const normalizeMaybeWrapped = (payload) => {
  if (payload && typeof payload === 'object' && payload.data !== undefined) {
    return payload.data
  }
  return payload
}

const normalizeTransactions = (payload) => {
  const unwrapped = normalizeMaybeWrapped(payload)
  if (Array.isArray(unwrapped)) {
    return { rows: unwrapped, meta: null }
  }
  if (unwrapped && typeof unwrapped === 'object') {
    if (Array.isArray(unwrapped.rows)) {
      return { rows: unwrapped.rows, meta: unwrapped.meta || null }
    }
    if (Array.isArray(unwrapped.transactions)) {
      return { rows: unwrapped.transactions, meta: unwrapped.meta || null }
    }
  }
  return { rows: [], meta: null }
}

export const fetchPersonalBalance = createAsyncThunk('billing/fetchPersonalBalance', async (_, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.getPersonalBalance(baseURL)
    return response
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to fetch personal balance') })
  }
})

export const fetchBillingSubscription = createAsyncThunk('billing/fetchBillingSubscription', async (_, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.getSubscription(baseURL)
    return response
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to fetch billing subscription') })
  }
})

export const fetchBillingTransactions = createAsyncThunk('billing/fetchBillingTransactions', async ({ limit = 20, offset = 0, direction = 'desc' } = {}, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.getTransactions({ limit, offset, direction }, baseURL)
    return response
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to fetch billing transactions') })
  }
})

const billingSlice = createSlice({
  name: 'billing',
  initialState,
  reducers: {
    clearBillingState: (state) => {
      Object.assign(state, initialState)
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchPersonalBalance.pending, (state) => {
        state.loadingBalance = true
        state.errorBalance = null
      })
      .addCase(fetchPersonalBalance.fulfilled, (state, action) => {
        state.loadingBalance = false
        state.balance = normalizeMaybeWrapped(action.payload?.data)
      })
      .addCase(fetchPersonalBalance.rejected, (state, action) => {
        state.loadingBalance = false
        state.errorBalance = action.payload?.message || action.error?.message || 'Failed to fetch personal balance'
      })
      .addCase(fetchBillingSubscription.pending, (state) => {
        state.loadingSubscription = true
        state.errorSubscription = null
      })
      .addCase(fetchBillingSubscription.fulfilled, (state, action) => {
        state.loadingSubscription = false
        state.subscription = normalizeMaybeWrapped(action.payload?.data)
      })
      .addCase(fetchBillingSubscription.rejected, (state, action) => {
        state.loadingSubscription = false
        state.errorSubscription = action.payload?.message || action.error?.message || 'Failed to fetch billing subscription'
      })
      .addCase(fetchBillingTransactions.pending, (state) => {
        state.loadingTransactions = true
        state.errorTransactions = null
      })
      .addCase(fetchBillingTransactions.fulfilled, (state, action) => {
        state.loadingTransactions = false
        const normalized = normalizeTransactions(action.payload?.data)
        state.transactions = normalized.rows
        state.transactionsMeta = normalized.meta
      })
      .addCase(fetchBillingTransactions.rejected, (state, action) => {
        state.loadingTransactions = false
        state.errorTransactions = action.payload?.message || action.error?.message || 'Failed to fetch billing transactions'
      })
  }
})

export const { clearBillingState } = billingSlice.actions
export default billingSlice.reducer
