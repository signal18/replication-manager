import { configureStore } from '@reduxjs/toolkit'
import authReducer from '../redux/authSlice'
import clusterReducer from '../redux/clusterSlice'
import globalClustersReducer from '../redux/globalClustersSlice'
import settingsReducer from '../redux/settingsSlice'
import configReducer from '../redux/configSlice'
import commonReducer from '../redux/commonSlice'
import toastReducer from '../redux/toastSlice'
import meetReducer from '../redux/meetSlice'
import { createLogger } from 'redux-logger'
import pathReducer from './pathSlice'

const middleware = []

if (import.meta.env.DEV) {
  middleware.push(createLogger())
}

export default configureStore({
  reducer: {
    auth: authReducer,
    cluster: clusterReducer,
    settings: settingsReducer,
    config: configReducer,
    common: commonReducer,
    toast: toastReducer,
    meet: meetReducer,
    globalClusters: globalClustersReducer,
    paths: pathReducer,
  },
  middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(middleware)
})
