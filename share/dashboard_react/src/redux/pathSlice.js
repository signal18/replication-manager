import { createSlice, createAsyncThunk, } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { pathService } from '../services/pathService'
import { isEqual } from 'lodash';
import { murmur3 } from 'murmurhash-js'


export const hashMurmur = (value) => murmur3(value, 18).toString(); // 18 from signal18

function shouldUseCachedTree(timestamp) {
  return timestamp && (Date.now() - timestamp < 900000);
}


export const getDockerTree = createAsyncThunk('settings/getDockerTree', async ({ clusterName, dockerImage }, thunkAPI) => {
  try {
    const dockerImageHash = hashMurmur(dockerImage)
    const oldState = thunkAPI.getState().paths
    if (oldState.dockerTreeList[dockerImageHash] && oldState.timestamps.dockerTree && oldState.timestamps.dockerTree[dockerImageHash] && shouldUseCachedTree(oldState.timestamps.dockerTree[dockerImageHash])) {
      return { data: oldState.dockerTreeList[dockerImageHash], status: 200 }
    }

    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await pathService.getDockerDirectoryTree(clusterName, dockerImage, baseURL)
    if (status === 200) {
      showSuccessBanner(`Fetching Docker directory tree successful!`, status, thunkAPI)
      return { data: data?.tree, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Fetching Docker directory tree failed!`, error, thunkAPI)
    return handleError(error, thunkAPI)
  }
},
  {
    condition: (_, { getState }) => {
      const state = getState()
      return !state.paths.loading.dockerTree
    }
  }
)

export const getGitTree = createAsyncThunk('settings/getGitTree', async ({ clusterName, appId, volumedir, repoURL }, thunkAPI) => {
  try {
    const gitRepoHash = hashMurmur(repoURL)
    const oldState = thunkAPI.getState().paths
    if (oldState.gitTreeList[gitRepoHash] && oldState.timestamps.gitTree && oldState.timestamps.gitTree[gitRepoHash] && shouldUseCachedTree(oldState.timestamps.gitTree[gitRepoHash])) {
      return { data: oldState.gitTreeList[gitRepoHash], status: 200 }
    }

    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await pathService.getGitDirectoryTree(clusterName, appId, volumedir, baseURL)
    if (status === 200) {
      showSuccessBanner(`Fetching Git directory tree successful!`, status, thunkAPI)
      return { data: data?.tree, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Fetching Git directory tree failed!`, error, thunkAPI)
    return handleError(error, thunkAPI)
  }
},
  {
    condition: (_, { getState }) => {
      const state = getState()
      return !state.paths.loading.gitTree
    }
  }
)

const initialState = {
  dockerTreeList: {},
  gitTreeList: {},
  timestamps: {
    dockerTree: {},
    gitTree: {}
  },
  current: {
    dockerHash: '',
    dockerTree: {},
    gitHash: '',
    gitTree: {},
  },
  loading: {
    dockerTree: false,
    gitTree: false
  },
  error: null
}

export const pathSlice = createSlice({
  name: 'paths',
  initialState,
  reducers: {
    clearPaths: () => (initialState),
    extendCache: (state, action) => {
      const { type, hash } = action.payload
      if (type === 'docker') {
        state.timestamps.dockerTree[hash] = Date.now()
      } else if (type === 'git') {
        state.timestamps.gitTree[hash] = Date.now()
      }
    },
    clearCache: (state, action) => {
      const { type, hash } = action.payload
      if (type === 'docker') {
        delete state.dockerTreeList[hash]
        delete state.timestamps.dockerTree[hash]
      } else if (type === 'git') {
        delete state.gitTreeList[hash]
        delete state.timestamps.gitTree[hash]
      }
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(getDockerTree.pending, (state) => {
        state.loading.dockerTree = true
      })
      .addCase(getDockerTree.fulfilled, (state, action) => {
        if (!action.payload) return
        const { data, status } = action.payload
        if (status === 200) {
          const dockerImageHash = hashMurmur(action.meta.arg.dockerImage)
          if (!isEqual(state.dockerTreeList[dockerImageHash], data)) {
            state.dockerTreeList[dockerImageHash] = data
            state.timestamps.dockerTree[dockerImageHash] = Date.now()
          } else if (!state.timestamps.dockerTree[dockerImageHash] || !shouldUseCachedTree(state.timestamps.dockerTree[dockerImageHash])) {
            state.timestamps.dockerTree[dockerImageHash] = Date.now()
          }
          state.current.dockerTree = data
          state.current.dockerHash = dockerImageHash
        }
        state.loading.dockerTree = false
      })
      .addCase(getDockerTree.rejected, (state, action) => {
        state.loading.dockerTree = false
        state.error = action.payload?.errorMessage || action.error.message
      })
      .addCase(getGitTree.pending, (state) => {
        state.loading.gitTree = true
      })
      .addCase(getGitTree.fulfilled, (state, action) => {
        if (!action.payload) return
        const { data, status } = action.payload
        if (status === 200) {
          const gitRepoHash = hashMurmur(action.meta.arg.repoURL)
          if (!isEqual(state.gitTreeList[gitRepoHash], data)) {
            state.gitTreeList[gitRepoHash] = data
            state.timestamps.gitTree[gitRepoHash] = Date.now()
          } else if (!state.timestamps.gitTree[gitRepoHash] || !shouldUseCachedTree(state.timestamps.gitTree[gitRepoHash])) {
            state.timestamps.gitTree[gitRepoHash] = Date.now()
          }
          state.current.gitTree = data
          state.current.gitHash = gitRepoHash
        }
        state.loading.gitTree = false
      })
      .addCase(getGitTree.rejected, (state, action) => {
        state.loading.gitTree = false
        state.error = action.payload?.errorMessage || action.error.message
      })
  }
})

export const { clearPaths, extendCache, clearCache } = pathSlice.actions

// this is for configureStore
export default pathSlice.reducer
