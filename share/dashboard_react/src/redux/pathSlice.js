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

export const getGitTree = createAsyncThunk('settings/getGitTree', async ({ clusterName, appId, gitName }, thunkAPI) => {
  try {
    const oldState = thunkAPI.getState().paths
    if (oldState.gitTreeList[gitName] && oldState.timestamps.gitTree && oldState.timestamps.gitTree[gitName] && shouldUseCachedTree(oldState.timestamps.gitTree[gitName])) {
      return { data: oldState.gitTreeList[gitName], status: 200 }
    }

    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await pathService.getGitDirectoryTree(clusterName, appId, gitName, baseURL)
    if (status === 200) {
      showSuccessBanner(`Fetching Git directory tree successful!`, status, thunkAPI)
      return { data: data?.tree, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    const rawMsg = error?.message || '';
    const isGitLabAuth = /\/api\/v4\//i.test(rawMsg) || /\b(401|unauthorized)\b/i.test(rawMsg);
    const displayError = isGitLabAuth
      ? new Error('GitLab API check failed: unauthorized. The Git tree browser requires a GitLab token with repository/API read access.')
      : error;
    showErrorBanner(`Fetching Git directory tree failed!`, displayError, thunkAPI)
    return handleError(displayError, thunkAPI)
  }
},
  {
    condition: (_, { getState }) => {
      const state = getState()
      return !state.paths.loading.gitTree
    }
  }
)

// checkGitRepo validates a new (unsaved) git clone using credentials from the form.
export const checkGitRepo = createAsyncThunk('settings/checkGitRepo', async ({ clusterName, appName, payload }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await pathService.checkGitRepo(clusterName, appName, payload, baseURL)
    if (status === 200) {
      return { data, status }
    } else {
      throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
    }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

// checkGitRepoByName validates an existing saved git clone using server-side
// decrypted credentials. The frontend never sends the password (which is masked).
export const checkGitRepoByName = createAsyncThunk('settings/checkGitRepoByName', async ({ clusterName, appId, gitName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await pathService.checkGitRepoByName(clusterName, appId, gitName, baseURL)
    if (status === 200) {
      return { data, status }
    } else {
      throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
    }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

const initialState = {
  dockerTreeList: {},
  gitTreeList: {},
  s3TreeList: {},
  timestamps: {
    dockerTree: {},
    gitTree: {},
    s3Tree: {}
  },
  current: {
    dockerHash: '',
    dockerTree: {},
    gitHash: '',
    gitTree: {},
    s3Hash: '',
    s3Tree: {},
  },
  loading: {
    dockerTree: false,
    gitTree: false,
    s3Tree: false
  },
  error: {
    docker: null,
    git: null,
    s3: null
  }
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
      } else if (type === 's3') {
        state.timestamps.s3Tree[hash] = Date.now()
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
      } else if (type === 's3') {
        delete state.s3TreeList[hash]
        delete state.timestamps.s3Tree[hash]
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
        state.error.docker = null
      })
      .addCase(getDockerTree.rejected, (state, action) => {
        state.loading.dockerTree = false
        state.error.docker = "Error getting docker tree"
      })
      .addCase(getGitTree.pending, (state) => {
        state.loading.gitTree = true
      })
      .addCase(getGitTree.fulfilled, (state, action) => {
        if (!action.payload) return
        const { data, status } = action.payload
        if (status === 200) {
          const gitName = action.meta.arg.gitName
          if (!isEqual(state.gitTreeList[gitName], data)) {
            state.gitTreeList[gitName] = data
            state.timestamps.gitTree[gitName] = Date.now()
          } else if (!state.timestamps.gitTree[gitName] || !shouldUseCachedTree(state.timestamps.gitTree[gitName])) {
            state.timestamps.gitTree[gitName] = Date.now()
          }
          state.current.gitTree = data
          state.current.gitHash = gitName
        }
        state.loading.gitTree = false
        state.error.git = null
      })
      .addCase(getGitTree.rejected, (state, action) => {
        state.loading.gitTree = false
        state.error.git = "Error getting git tree"
      })
  }
})

export const { clearPaths, extendCache, clearCache } = pathSlice.actions

// this is for configureStore
export default pathSlice.reducer
