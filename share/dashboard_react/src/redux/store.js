import { configureStore } from '@reduxjs/toolkit'
import authReducer from '../redux/authSlice'
import clusterReducer from '../redux/clusterSlice'
import globalClustersReducer from '../redux/globalClustersSlice'
import settingsReducer from '../redux/settingsSlice'
import configReducer from '../redux/configSlice'
import commonReducer from '../redux/commonSlice'
import toastReducer from '../redux/toastSlice'
import logger from 'redux-logger'

export default configureStore({
  reducer: {
    auth: authReducer,
    cluster: clusterReducer,
    settings: settingsReducer,
    config: configReducer,
    common: commonReducer,
    toast: toastReducer,
    globalClusters: globalClustersReducer
  },
  middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(logger)
})
