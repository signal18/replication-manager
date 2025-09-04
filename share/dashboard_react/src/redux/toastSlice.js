import { createSlice } from '@reduxjs/toolkit'

const toastSlice = createSlice({
  name: 'toast',
  initialState: {
    status: '',
    title: '',
    description: ''
  },
  reducers: {
    showWarningToast: (state, action) => {
      state.status = 'warning'
      state.title = action.payload.title || ''
      state.description = action.payload.description || ''
    },
    showSuccessToast: (state, action) => {
      state.status = 'success'
      state.title = action.payload.title || ''
      state.description = action.payload.description || ''
    },
    showErrorToast: (state, action) => {
      state.status = 'error'
      state.title = action.payload.title || ''
      state.description = action.payload.description || ''
    },
    showLoadingToast: (state, action) => {
      state.status = 'info'
      state.title = action.payload.title || ''
      state.description = action.payload.description || ''
    },
    resetToast: (state) => {
      state.status = null
      state.title = ''
      state.description = ''
    }
  }
})

export const { showSuccessToast, showWarningToast, showErrorToast, showLoadingToast, resetToast } = toastSlice.actions

export default toastSlice.reducer
