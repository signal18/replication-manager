import { createSlice } from '@reduxjs/toolkit'

const toastSlice = createSlice({
  name: 'toast',
  initialState: {
    status: '',
    title: '',
    description: ''
  },
  reducers: {
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
    resetToast: (state) => {
      state.status = null
      state.title = ''
      state.description = ''
    }
  }
})

export const { showSuccessToast, showErrorToast, resetToast } = toastSlice.actions

export default toastSlice.reducer
