import { useEffect, useMemo, useState, memo, useCallback, useRef } from 'react'
import PropTypes from 'prop-types'
import { Box, Checkbox, FormControl, FormHelperText, FormLabel, HStack, Input, Select, Text, Textarea, VStack } from '@chakra-ui/react'

const QueueMoveForm = memo(({ list = [], currentId, onChange = () => { } }) => {
  const [direction, setDirection] = useState('first')

  const handleDirectionChange = (e) => {
    setDirection(e.target.value)
    if (e.target.value !== 'after') {
      onChange(e.target.value, null)
    }
  }

  const handleAfterChange = (e) => {
    onChange('after', e.target.value)
  }

  return (
    <VStack>
      <HStack>
        <Box>Move {direction}:</Box>
        <Select onChange={handleDirectionChange} value={direction}>
          <option value="first">First</option>
          <option value="before">After</option>
          <option value="last">Last</option>
        </Select>
      </HStack>
      {direction === 'after' && (
        <HStack>
          <Box>Move After:</Box>
          <Select onChange={handleAfterChange}>
            {list
              .filter((item) => item.task_id !== currentId)
              .map((item) => (
                <option key={item.task_id} value={item.task_id}>
                  Task ID #{item.task_id}
                </option>
              ))}
          </Select>
        </HStack>
      )}
    </VStack>
  )
})

QueueMoveForm.displayName = 'QueueMoveForm'

const buildPathTrees = (entries, basePaths) => {
  const normalizePath = (value) => {
    if (!value || typeof value !== 'string') {
      return ''
    }
    let trimmed = value.trim()
    if (!trimmed) {
      return ''
    }
    if (!trimmed.startsWith('/')) {
      trimmed = `/${trimmed}`
    }
    return trimmed.replace(/\/+$/, '') || '/'
  }

  const bases = (basePaths || [])
    .map((base) => normalizePath(base))
    .filter(Boolean)

  if (bases.length === 0) {
    return []
  }

  const sortedBases = [...bases].sort((a, b) => b.length - a.length)
  const roots = new Map()

  sortedBases.forEach((base) => {
    if (!roots.has(base)) {
      roots.set(base, { name: base, path: base, type: 'dir', children: [] })
    }
  })

  const addNode = (root, relativeParts, entry) => {
    let current = root
    relativeParts.forEach((part, index) => {
      const isLeaf = index === relativeParts.length - 1
      const fullPath = root.path === '/'
        ? `/${relativeParts.slice(0, index + 1).join('/')}`
        : `${root.path}/${relativeParts.slice(0, index + 1).join('/')}`
      let child = current.children.find((node) => node.name === part)
      if (!child) {
        child = { name: part, path: fullPath, type: isLeaf ? entry.type || 'file' : 'dir', children: [] }
        current.children.push(child)
      } else if (isLeaf && entry.type) {
        child.type = entry.type
      }
      current = child
    })
  }

  entries.forEach((entry) => {
    const entryPath = normalizePath(entry?.path)
    if (!entryPath) {
      return
    }
    const base = sortedBases.find((candidate) => candidate === '/' || entryPath === candidate || entryPath.startsWith(`${candidate}/`))
    if (!base) {
      return
    }
    if (entryPath === base) {
      if (entry?.type === 'file') {
        const root = roots.get(base)
        if (root) {
          const name = entryPath.split('/').filter(Boolean).pop() || entryPath
          root.children.push({ name, path: entryPath, type: 'file', children: [] })
        }
      }
      return
    }
    const relative = base === '/' ? entryPath.replace(/^\//, '') : entryPath.slice(base.length + 1)
    const parts = relative.split('/').filter(Boolean)
    if (parts.length === 0) {
      return
    }
    const root = roots.get(base)
    if (root) {
      addNode(root, parts, entry)
    }
  })

  const sortNodes = (node) => {
    node.children.sort((a, b) => a.name.localeCompare(b.name))
    node.children.forEach(sortNodes)
  }
  roots.forEach(sortNodes)

  return Array.from(roots.values())
}

const resticOverwriteOptions = [
  { label: 'Use restic default', value: '' },
  { label: 'Overwrite always', value: 'always' },
  { label: 'Overwrite if changed', value: 'if-changed' },
  { label: 'Overwrite if newer', value: 'if-newer' },
  { label: 'Never overwrite', value: 'never' }
]

const RestorePathTree = memo(({ pathTrees, renderTreeNodes, isLoading, loadError }) => (
  <FormControl>
    <FormLabel>Available paths</FormLabel>
    <FormHelperText>Select entries to populate the Paths list (scoped to the base path).</FormHelperText>
    {isLoading && <Box>Loading snapshot contents...</Box>}
    {!isLoading && loadError && <Box color="red.500">{loadError}</Box>}
    {!isLoading && !loadError && pathTrees.length === 0 && <Box>No snapshot paths loaded.</Box>}
    {!isLoading && pathTrees.length > 0 && (
      <Box maxH="240px" overflowY="auto" borderWidth="1px" borderRadius="md" padding={2}>
        <VStack align="start" spacing={3}>
          {pathTrees.map((root) => (
            <Box key={root.path} width="100%">
              <Text fontWeight="bold" mb={1}>Contents of {root.path}</Text>
              {renderTreeNodes(root.children)}
            </Box>
          ))}
        </VStack>
      </Box>
    )}
  </FormControl>
))

RestorePathTree.displayName = 'RestorePathTree'

const SnapshotRestoreForm = memo(({
  basePath = '',
  targetDir = '',
  overwrite = '',
  paths = [],
  availablePaths = [],
  basePaths = [],
  isLoading = false,
  loadError = null,
  onBasePathChange = () => { },
  onTargetChange = () => { },
  onOverwriteChange = () => { },
  onPathsChange = () => { }
}) => {
  const [pathsText, setPathsText] = useState(Array.isArray(paths) ? paths.join('\n') : '')
  const selectedPaths = useMemo(() => (Array.isArray(paths) ? paths : []), [paths])
  const [useSpecificPaths, setUseSpecificPaths] = useState(Array.isArray(paths) && paths.length > 0)
  const [cachedPaths, setCachedPaths] = useState(selectedPaths)
  
  // Use ref to always have the latest paths value
  const pathsRef = useRef(paths)
  
  useEffect(() => {
    pathsRef.current = paths
  }, [paths])
  const normalizedBasePaths = useMemo(
    () => (Array.isArray(basePaths) ? basePaths.filter((path) => typeof path === 'string' && path.trim()) : []),
    [basePaths]
  )
  const treeBasePaths = useMemo(() => {
    if (basePath && typeof basePath === 'string') {
      return [basePath]
    }
    return normalizedBasePaths
  }, [basePath, normalizedBasePaths])
  const pathTrees = useMemo(() => {
    if (!useSpecificPaths) {
      return []
    }
    return buildPathTrees(availablePaths, treeBasePaths)
  }, [availablePaths, treeBasePaths, useSpecificPaths])
  const selectedSet = useMemo(() => new Set(selectedPaths), [selectedPaths])

  useEffect(() => {
    const nextPaths = Array.isArray(paths) ? paths : []
    setPathsText(nextPaths.join('\n'))
    if (nextPaths.length > 0) {
      setUseSpecificPaths(true)
      setCachedPaths(nextPaths)
    }
  }, [paths])

  useEffect(() => {
    if (!basePath && normalizedBasePaths.length === 1) {
      onBasePathChange(normalizedBasePaths[0])
    }
  }, [basePath, normalizedBasePaths, onBasePathChange])

  const handlePathsChange = (value) => {
    setPathsText(value)
    const parsed = value
      .split(/\r?\n|,/)
      .map((entry) => entry.trim())
      .filter(Boolean)
    setCachedPaths(parsed)
    onPathsChange(parsed)
  }

  const updateSelectedPaths = useCallback((nextList) => {
    setPathsText(nextList.join('\n'))
    setCachedPaths(nextList)
    onPathsChange(nextList)
  }, [onPathsChange])

  const collectDescendantPaths = useCallback((node) => {
    const paths = []
    const walk = (current) => {
      paths.push(current.path)
      current.children?.forEach(walk)
    }
    walk(node)
    return paths
  }, [])

  const toggleNodeSelection = useCallback((node, state, isDisabled) => {
    if (isDisabled) {
      return
    }
    // Use ref to get the latest paths value, avoiding stale closures
    const currentPaths = Array.isArray(pathsRef.current) ? pathsRef.current : []
    const nextSet = new Set(currentPaths)
    const descendantPaths = collectDescendantPaths(node)
    
    if (state.checked) {
      descendantPaths.forEach((path) => nextSet.delete(path))
    } else {
      nextSet.add(node.path)
      descendantPaths.forEach((path) => {
        if (path !== node.path) {
          nextSet.delete(path)
        }
      })
    }
    updateSelectedPaths(Array.from(nextSet))
  }, [collectDescendantPaths, updateSelectedPaths])

  const getNodeState = useCallback((node, currentSelectedSet) => {
    const isExplicit = currentSelectedSet.has(node.path)
    if (!node.children?.length) {
      return { checked: isExplicit, indeterminate: false }
    }
    const childStates = node.children.map((child) => getNodeState(child, currentSelectedSet))
    const allChildrenSelected = childStates.every((child) => child.checked)
    const someChildrenSelected = childStates.some((child) => child.checked || child.indeterminate)
    const checked = isExplicit || allChildrenSelected
    const indeterminate = !isExplicit && someChildrenSelected && !allChildrenSelected
    return { checked, indeterminate }
  }, [])

  const handleToggleSpecificPaths = (e) => {
    const isEnabled = e.target.checked
    setUseSpecificPaths(isEnabled)
    if (isEnabled) {
      setPathsText(cachedPaths.join('\n'))
      onPathsChange(cachedPaths)
    } else {
      setPathsText('')
      onPathsChange([])
    }
  }

  const renderTreeNodes = useCallback((nodes, depth = 0, ancestorSelected = false) => (
    nodes.map((node) => {
      const state = getNodeState(node, selectedSet)
      const isExplicit = selectedSet.has(node.path)
      const isCovered = ancestorSelected || isExplicit
      const checked = ancestorSelected || state.checked
      const indeterminate = !ancestorSelected && state.indeterminate
      const isDisabled = ancestorSelected && !isExplicit
      // Generate unique ID for each checkbox to prevent duplicate IDs
      const checkboxId = `checkbox-${node.path.replace(/[^a-zA-Z0-9]/g, '-')}`

      return (
        <Box key={node.path} pl={`${depth * 16}px`}>
          <HStack spacing={2}>
            <Checkbox
              id={checkboxId}
              isChecked={checked}
              isIndeterminate={indeterminate}
              isDisabled={isDisabled}
              onChange={() => toggleNodeSelection(node, state, isDisabled)}
            >
              {node.name}{node.type ? ` (${node.type})` : ''}
            </Checkbox>
          </HStack>
          {node.children?.length > 0 && renderTreeNodes(node.children, depth + 1, isCovered)}
        </Box>
      )
    })
  ), [selectedSet, getNodeState, toggleNodeSelection])

  return (
    <VStack align="stretch">
      <FormControl>
        <FormLabel>Base path (subfolder)</FormLabel>
        {normalizedBasePaths.length > 0 ? (
          <Select value={basePath || ''} onChange={(e) => onBasePathChange(e.target.value)}>
            <option value="">Use snapshot root (no subfolder)</option>
            {normalizedBasePaths.map((path) => (
              <option key={path} value={path}>
                {path}
              </option>
            ))}
          </Select>
        ) : (
          <Input
            value={basePath}
            onChange={(e) => onBasePathChange(e.target.value)}
            placeholder="/var/lib/mysql"
          />
        )}
        <FormHelperText>
          Used as the snapshot subfolder ({'<snapshot>:<subfolder>'}). Leave empty to allow includes across base paths.
        </FormHelperText>
      </FormControl>
      <FormControl>
        <FormLabel>Target directory</FormLabel>
        <Input
          value={targetDir}
          onChange={(e) => onTargetChange(e.target.value)}
          placeholder="/"
        />
        <FormHelperText>Target directory for restic restore (--target). Defaults to the source path.</FormHelperText>
      </FormControl>
      <FormControl>
        <FormLabel>Overwrite policy</FormLabel>
        <Select value={overwrite} onChange={(e) => onOverwriteChange(e.target.value)}>
          {resticOverwriteOptions.map((option) => (
            <option key={option.label} value={option.value}>
              {option.label}
            </option>
          ))}
        </Select>
        <FormHelperText>Controls restic --overwrite behavior when restoring in place.</FormHelperText>
      </FormControl>
      <FormControl>
        <Checkbox isChecked={useSpecificPaths} onChange={handleToggleSpecificPaths}>
          Restore specific paths
        </Checkbox>
        <FormHelperText>Turn off to restore the entire snapshot under the target directory.</FormHelperText>
      </FormControl>
      {useSpecificPaths && (
        <>
          <FormControl>
            <FormLabel>Paths to restore</FormLabel>
            <Textarea
              value={pathsText}
              onChange={(e) => handlePathsChange(e.target.value)}
              placeholder="/var/lib/mysql\n/etc/mysql"
            />
            <FormHelperText>Each entry becomes a restic --include pattern and will be restored under the target directory.</FormHelperText>
          </FormControl>
          <RestorePathTree
            pathTrees={pathTrees}
            renderTreeNodes={renderTreeNodes}
            isLoading={isLoading}
            loadError={loadError}
          />
        </>
      )}
    </VStack>
  )
})

SnapshotRestoreForm.displayName = 'SnapshotRestoreForm'

const formConfig = {
  queueMove: {
    component: QueueMoveForm,
    getProps: ({ queueData, payload, onMove }) => ({
      list: queueData,
      currentId: payload?.data?.taskId,
      onChange: onMove
    })
  },
  snapshotRestore: {
    component: SnapshotRestoreForm,
    getProps: ({ payload, onRestoreBasePath, onRestoreTarget, onRestoreOverwrite, onRestorePaths, restorePaths, restorePathsLoading, restorePathsError }) => ({
      basePath: payload?.data?.basePath,
      targetDir: payload?.data?.targetDir,
      overwrite: payload?.data?.overwrite,
      paths: payload?.data?.paths,
      availablePaths: restorePaths,
      basePaths: payload?.data?.basePaths,
      isLoading: restorePathsLoading,
      loadError: restorePathsError,
      onBasePathChange: onRestoreBasePath,
      onTargetChange: onRestoreTarget,
      onOverwriteChange: onRestoreOverwrite,
      onPathsChange: onRestorePaths
    })
  }
}

function ConfirmActionForm({
  payload,
  queueData,
  onMove,
  onRestoreBasePath = () => { },
  onRestoreTarget = () => { },
  onRestoreOverwrite = () => { },
  onRestorePaths = () => { },
  restorePaths = [],
  restorePathsLoading = false,
  restorePathsError = null
}) {
  if (!payload) return null

  const entry = useMemo(() => formConfig[payload.action], [payload.action])
  
  if (!entry) return null

  const Component = entry.component
  const props = entry.getProps({
    payload,
    queueData,
    onMove,
    onRestoreBasePath,
    onRestoreTarget,
    onRestoreOverwrite,
    onRestorePaths,
    restorePaths,
    restorePathsLoading,
    restorePathsError
  })

  return <Component {...props} />
}

export default ConfirmActionForm

QueueMoveForm.propTypes = {
  list: PropTypes.arrayOf(PropTypes.object),
  currentId: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  onChange: PropTypes.func
}

SnapshotRestoreForm.propTypes = {
  basePath: PropTypes.string,
  targetDir: PropTypes.string,
  overwrite: PropTypes.string,
  paths: PropTypes.arrayOf(PropTypes.string),
  availablePaths: PropTypes.arrayOf(
    PropTypes.shape({
      path: PropTypes.string,
      type: PropTypes.string,
      size: PropTypes.number
    })
  ),
  basePaths: PropTypes.arrayOf(PropTypes.string),
  isLoading: PropTypes.bool,
  loadError: PropTypes.string,
  onBasePathChange: PropTypes.func,
  onTargetChange: PropTypes.func,
  onOverwriteChange: PropTypes.func,
  onPathsChange: PropTypes.func
}

ConfirmActionForm.propTypes = {
  payload: PropTypes.shape({
    action: PropTypes.string,
    data: PropTypes.object
  }),
  queueData: PropTypes.arrayOf(PropTypes.object),
  onMove: PropTypes.func,
  onRestoreBasePath: PropTypes.func,
  onRestoreTarget: PropTypes.func,
  onRestoreOverwrite: PropTypes.func,
  onRestorePaths: PropTypes.func,
  restorePaths: PropTypes.arrayOf(
    PropTypes.shape({
      path: PropTypes.string,
      type: PropTypes.string,
      size: PropTypes.number
    })
  ),
  restorePathsLoading: PropTypes.bool,
  restorePathsError: PropTypes.string
}
