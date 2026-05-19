import { Box, Button, Flex, HStack, Select, Text, VStack, Code, Badge } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { useSelector } from 'react-redux'
import { clusterService } from '../../services/clusterService'
import styles from './styles.module.scss'

const COMMENT_COLORS = {
  'preserved:server-specific': { bg: '#e3f2fd', color: '#1565c0', label: 'Server Preserved' },
  'preserved:cluster-level': { bg: '#e0f7fa', color: '#00838f', label: 'Cluster Preserved' },
  'preserved:legacy-migrated': { bg: '#f5f5f5', color: '#616161', label: 'Legacy Migrated' },
  'preserved:unknown': { bg: '#ede7f6', color: '#4527a0', label: 'Preserved' },
  'delta:no-config': { bg: '#fff3e0', color: '#e65100', label: 'Deprecated (loose)' },
  'delta:value-differs': { bg: '#fffde7', color: '#f57f17', label: 'Value Differs' },
  'agreed:accepted': { bg: '#e8f5e9', color: '#2e7d32', label: 'Accepted' },
  'agreed:dropped': { bg: '#ffebee', color: '#c62828', label: 'Dropped' },
}

function parseConfigLines(content) {
  if (!content) return []
  const lines = content.split('\n')
  const result = []
  let currentComment = null

  for (const line of lines) {
    const trimmed = line.trim()
    if (trimmed === '' || trimmed === '[mysqld]') continue

    // Check if this is an origin comment
    if (trimmed.startsWith('#')) {
      const commentBody = trimmed.substring(1).trim()
      // Match known origin prefixes
      for (const prefix of Object.keys(COMMENT_COLORS)) {
        if (commentBody.startsWith(prefix)) {
          currentComment = prefix
          break
        }
      }
      // Dropped variables are shown as comments
      if (currentComment === 'agreed:dropped') {
        const varName = commentBody.replace('agreed:dropped', '').trim()
        if (varName) continue // skip the "agreed:dropped" line, next line has the var name
        result.push({ type: 'dropped', comment: currentComment, text: trimmed })
        currentComment = null
      }
      continue
    }

    // Regular variable line
    const [varName, ...valueParts] = trimmed.split('=')
    const value = valueParts.join('=')
    const displayName = varName?.trim().replace(/^loose_/, '')
    const isLoose = varName?.trim().startsWith('loose_')

    result.push({
      type: 'variable',
      comment: currentComment,
      name: displayName,
      value: value?.trim() || '',
      isLoose,
      raw: trimmed,
    })
    currentComment = null
  }
  return result
}

function FilePanel({ title, content, emptyText }) {
  const lines = parseConfigLines(content)

  return (
    <Box className={styles.filePanel}>
      <Text className={styles.fileTitle}>{title}</Text>
      <Box className={styles.fileContent}>
        {lines.length === 0 ? (
          <Text className={styles.emptyText}>{emptyText || 'Empty'}</Text>
        ) : (
          lines.map((line, idx) => {
            const colors = line.comment ? COMMENT_COLORS[line.comment] : null
            if (line.type === 'dropped') {
              return (
                <Flex key={idx} className={styles.fileLine} bg={COMMENT_COLORS['agreed:dropped'].bg}>
                  <Badge colorScheme='red' size='sm' mr={2}>Dropped</Badge>
                  <Code className={styles.lineText} color={COMMENT_COLORS['agreed:dropped'].color}>
                    {line.text.replace('#', '').trim()}
                  </Code>
                </Flex>
              )
            }
            return (
              <Flex key={idx} className={styles.fileLine} bg={colors?.bg || 'transparent'}>
                {colors && (
                  <Badge size='sm' mr={2} bg={colors.bg} color={colors.color} border='1px solid' borderColor={colors.color}>
                    {colors.label}
                  </Badge>
                )}
                {line.isLoose && (
                  <Badge colorScheme='orange' size='sm' mr={2}>loose</Badge>
                )}
                <Code className={styles.lineText}>
                  <Text as='span' fontWeight='bold'>{line.name}</Text>
                  <Text as='span'> = {line.value}</Text>
                </Code>
              </Flex>
            )
          })
        )}
      </Box>
    </Box>
  )
}

function ConfigFilesPanel({ selectedCluster }) {
  const [selectedServer, setSelectedServer] = useState('')
  const [configFiles, setConfigFiles] = useState({ preserved: '', delta: '', agreed: '' })
  const [loading, setLoading] = useState(false)
  const [refreshKey, setRefreshKey] = useState(0)
  const { auth: { baseURL }, cluster: { clusterServers } } = useSelector((state) => state)

  const servers = clusterServers || []

  useEffect(() => {
    if (servers.length > 0 && !selectedServer) {
      setSelectedServer(servers[0]?.id || '')
    }
  }, [servers])

  useEffect(() => {
    if (selectedCluster?.name && selectedServer) {
      setLoading(true)
      clusterService.getServerConfigFiles(selectedCluster.name, selectedServer, baseURL)
        .then(({ data }) => setConfigFiles(data))
        .catch(() => setConfigFiles({ preserved: '', delta: '', agreed: '' }))
        .finally(() => setLoading(false))
    }
  }, [selectedCluster?.name, selectedServer, refreshKey])

  const handleClearDelta = () => {
    if (!selectedCluster?.name || !selectedServer) return
    clusterService.clearServerDelta(selectedCluster.name, selectedServer, baseURL)
      .then(() => setRefreshKey((k) => k + 1))
      .catch(() => {})
  }

  return (
    <VStack className={styles.container} align='stretch' spacing={3}>
      <HStack>
        <Text fontWeight='bold'>Config Override Files</Text>
        <Select
          size='sm'
          width='300px'
          value={selectedServer}
          onChange={(e) => setSelectedServer(e.target.value)}
        >
          {servers.map((srv) => (
            <option key={srv.id} value={srv.id}>{srv.host}:{srv.port}</option>
          ))}
        </Select>
      </HStack>

      {loading ? (
        <Text>Loading...</Text>
      ) : (
        <VStack spacing={3} align='stretch'>
          <FilePanel
            title='01_preserved.cnf'
            content={configFiles.preserved}
            emptyText='No preserved variables'
          />
          <Box className={styles.filePanel}>
            <Flex className={styles.fileTitle} justify='space-between' align='center'>
              <Text fontWeight={600} fontSize='0.85rem' fontFamily='monospace'>02_delta.cnf</Text>
              {configFiles.delta && parseConfigLines(configFiles.delta).length > 0 && (
                <Button size='xs' colorScheme='red' variant='outline' onClick={handleClearDelta}>
                  Clear Delta
                </Button>
              )}
            </Flex>
            <Box className={styles.fileContent}>
              {parseConfigLines(configFiles.delta).length === 0 ? (
                <Text className={styles.emptyText}>No delta — server is fully converged</Text>
              ) : (
                parseConfigLines(configFiles.delta).map((line, idx) => {
                  const colors = line.comment ? COMMENT_COLORS[line.comment] : null
                  if (line.type === 'dropped') {
                    return (
                      <Flex key={idx} className={styles.fileLine} bg={COMMENT_COLORS['agreed:dropped'].bg}>
                        <Badge colorScheme='red' size='sm' mr={2}>Dropped</Badge>
                        <Code className={styles.lineText} color={COMMENT_COLORS['agreed:dropped'].color}>
                          {line.text.replace('#', '').trim()}
                        </Code>
                      </Flex>
                    )
                  }
                  return (
                    <Flex key={idx} className={styles.fileLine} bg={colors?.bg || 'transparent'}>
                      {colors && (
                        <Badge size='sm' mr={2} bg={colors.bg} color={colors.color} border='1px solid' borderColor={colors.color}>
                          {colors.label}
                        </Badge>
                      )}
                      {line.isLoose && (
                        <Badge colorScheme='orange' size='sm' mr={2}>loose</Badge>
                      )}
                      <Code className={styles.lineText}>
                        <Text as='span' fontWeight='bold'>{line.name}</Text>
                        <Text as='span'> = {line.value}</Text>
                      </Code>
                    </Flex>
                  )
                })
              )}
            </Box>
          </Box>
          <FilePanel
            title='03_agreed.cnf'
            content={configFiles.agreed}
            emptyText='No accepted deviations'
          />
        </VStack>
      )}
    </VStack>
  )
}

export default ConfigFilesPanel
