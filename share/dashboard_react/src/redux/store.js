import { configureStore } from '@reduxjs/toolkit'
import authReducer from '../redux/authSlice'
import clusterReducer from '../redux/clusterSlice'
import settingsReducer from '../redux/settingsSlice'
import commonReducer from '../redux/commonSlice'
import toastReducer from '../redux/toastSlice'
import logger from 'redux-logger'

export default configureStore({
  reducer: {
    auth: authReducer,
    cluster: clusterReducer,
    settings: settingsReducer,
    common: commonReducer,
    toast: toastReducer
  },
  middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(logger)
})
