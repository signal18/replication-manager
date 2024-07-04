import { configureStore } from '@reduxjs/toolkit'
import authReducer from '../redux/authSlice'
import clusterReducer from '../redux/clusterSlice'
import logger from 'redux-logger'

export default configureStore({
  reducer: {
    auth: authReducer,
    cluster: clusterReducer
  },
  middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(logger)
})
