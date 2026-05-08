import { createAsyncThunk, createSlice } from '@reduxjs/toolkit'
import { billingService } from '../services/billingService'
import { extractApiErrorMessage } from '../utils/apiError'

const initialState = {
  balance: null,
  subscription: null,
  plansCatalog: [],
  changeSubscriptionResult: null,
  transactions: [],
  transactionsMeta: null,
  loadingBalance: false,
  loadingSubscription: false,
  loadingPlansCatalog: false,
  loadingChangeSubscription: false,
  loadingTransactions: false,
  errorBalance: null,
  errorSubscription: null,
  errorPlansCatalog: null,
  errorChangeSubscription: null,
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

const normalizeBillingErrorMessage = (response, fallbackMessage, crmUnavailableMessage) => {
  const status = Number(response?.status || 0)
  if (status === 502) {
    return crmUnavailableMessage
  }

  const apiMessage = typeof response?.data === 'object'
    ? (response.data?.error || response.data?.message)
    : ''

  return apiMessage || fallbackMessage
}

export const fetchPersonalBalance = createAsyncThunk('billing/fetchPersonalBalance', async (_, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.getPersonalBalance(baseURL)
    const status = Number(response?.status || 0)
    if (status >= 200 && status < 300) {
      return response
    }

    return thunkAPI.rejectWithValue({
      message: normalizeBillingErrorMessage(
        response,
        'Failed to fetch personal balance',
        'The CRM billing service is temporarily unavailable. Personal balance cannot be loaded right now.'
      )
    })
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to fetch personal balance') })
  }
})

export const fetchBillingSubscription = createAsyncThunk('billing/fetchBillingSubscription', async (_, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.getSubscription(baseURL)
    const status = Number(response?.status || 0)
    if (status >= 200 && status < 300) {
      return response
    }

    return thunkAPI.rejectWithValue({
      message: normalizeBillingErrorMessage(
        response,
        'Failed to fetch billing subscription',
        'The DBaaS subscription service is temporarily unavailable. Subscription details cannot be loaded right now.'
      )
    })
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to fetch billing subscription') })
  }
})

export const fetchBillingPlansCatalog = createAsyncThunk('billing/fetchBillingPlansCatalog', async (_, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.getSubscriptionPlans(baseURL)
    const status = Number(response?.status || 0)
    if (status >= 200 && status < 300) {
      return response
    }

    return thunkAPI.rejectWithValue({
      message: normalizeBillingErrorMessage(
        response,
        'Failed to fetch billing plans catalog',
        'The DBaaS plans catalog service is temporarily unavailable. Plan choices cannot be loaded right now.'
      )
    })
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to fetch billing plans catalog') })
  }
})

export const fetchBillingTransactions = createAsyncThunk('billing/fetchBillingTransactions', async ({ limit = 20, offset = 0, direction = 'desc' } = {}, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.getTransactions({ limit, offset, direction }, baseURL)
    const status = Number(response?.status || 0)
    if (status >= 200 && status < 300) {
      return response
    }

    return thunkAPI.rejectWithValue({
      message: normalizeBillingErrorMessage(
        response,
        'Failed to fetch billing transactions',
        'The CRM billing ledger service is temporarily unavailable. Transactions cannot be loaded right now.'
      )
    })
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to fetch billing transactions') })
  }
})

export const requestBillingSubscriptionChange = createAsyncThunk('billing/requestBillingSubscriptionChange', async ({ subscription }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const response = await billingService.changeSubscriptionPlan(subscription, baseURL)
    const status = Number(response?.status || 0)
    if (status === 200 || status === 202) {
      return response
    }

    if (status === 502) {
      return thunkAPI.rejectWithValue({
        message: 'The DBaaS subscription service is temporarily unavailable. Your subscription was not changed. Please try again shortly.'
      })
    }

    const apiMessage = typeof response?.data === 'object'
      ? (response.data?.error || response.data?.message)
      : ''

    return thunkAPI.rejectWithValue({ message: apiMessage || 'Failed to change billing subscription' })
  } catch (error) {
    return thunkAPI.rejectWithValue({ message: extractApiErrorMessage(error, 'Failed to change billing subscription') })
  }
})

const billingSlice = createSlice({
  name: 'billing',
  initialState,
  reducers: {
    clearBillingState: (state) => {
      Object.assign(state, initialState)
    },
    clearChangeSubscriptionError: (state) => {
      state.errorChangeSubscription = null
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
      .addCase(fetchBillingPlansCatalog.pending, (state) => {
        state.loadingPlansCatalog = true
        state.errorPlansCatalog = null
      })
      .addCase(fetchBillingPlansCatalog.fulfilled, (state, action) => {
        state.loadingPlansCatalog = false
        state.plansCatalog = normalizeMaybeWrapped(action.payload?.data)
      })
      .addCase(fetchBillingPlansCatalog.rejected, (state, action) => {
        state.loadingPlansCatalog = false
        state.errorPlansCatalog = action.payload?.message || action.error?.message || 'Failed to fetch billing plans catalog'
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
      .addCase(requestBillingSubscriptionChange.pending, (state) => {
        state.loadingChangeSubscription = true
        state.errorChangeSubscription = null
      })
      .addCase(requestBillingSubscriptionChange.fulfilled, (state, action) => {
        state.loadingChangeSubscription = false
        state.errorChangeSubscription = null
        state.changeSubscriptionResult = normalizeMaybeWrapped(action.payload?.data)
      })
      .addCase(requestBillingSubscriptionChange.rejected, (state, action) => {
        state.loadingChangeSubscription = false
        state.errorChangeSubscription = action.payload?.message || action.error?.message || 'Failed to change billing subscription'
      })
  }
})

export const { clearBillingState, clearChangeSubscriptionError } = billingSlice.actions
export default billingSlice.reducer
