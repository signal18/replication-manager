import {
  Alert,
  AlertIcon,
  Box,
  List,
  ListItem,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Text
} from '@chakra-ui/react'
import { useState } from 'react'
import { useDispatch } from 'react-redux'
import RMButton from '../RMButton'
import NotFound from '../NotFound'
import parentStyles from './styles.module.scss'
import { useTheme } from '../../ThemeProvider'
import { fetchDynamicClustersFromGit, getClusters, getMonitoredData } from '../../redux/globalClustersSlice'
import { extractApiErrorMessage } from '../../utils/apiError'
import PropTypes from 'prop-types'

const RESULT_GROUPS = [
  { key: 'imported', label: 'Imported', colorScheme: 'green' },
  { key: 'skipped_existing', label: 'Skipped Existing', colorScheme: 'gray' },
  { key: 'invalid', label: 'Invalid', colorScheme: 'orange' }
]

function DynamicClusterGitImportModal({ isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()

  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState(null)
  const [inlineError, setInlineError] = useState('')

  const hasResult = Boolean(result)

  const handleImport = async () => {
    setLoading(true)
    setInlineError('')
    try {
      const { data } = await dispatch(fetchDynamicClustersFromGit()).unwrap()
      setResult(data)
      dispatch(getClusters({}))
      dispatch(getMonitoredData({}))
    } catch (error) {
      setInlineError(extractApiErrorMessage(error, 'Dynamic cluster import failed.'))
    } finally {
      setLoading(false)
    }
  }

  const handleClose = () => {
    if (loading) return
    setResult(null)
    setInlineError('')
    closeModal()
  }

  const errorEntries = Object.entries(result?.errors || {}).sort(([a], [b]) => a.localeCompare(b))

  return (
    <Modal isOpen={isOpen} onClose={handleClose} closeOnOverlayClick={!loading} closeOnEsc={!loading}>
      <ModalOverlay />
      <ModalContent
        className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
        width='60%'
        maxWidth='none'
        minHeight='200px'
        maxH='90%'
        overflow='hidden'>
        <ModalHeader>Import Dynamic Clusters from Git</ModalHeader>
        <ModalCloseButton isDisabled={loading} />
        <ModalBody overflowY='auto' pb={6}>
          {inlineError && (
            <Alert status='error' borderRadius='md' mb={4}>
              <AlertIcon />
              {inlineError}
            </Alert>
          )}

          {!hasResult && !inlineError && (
            <Box>
              <Text fontSize='sm' mb={2}>
                Imports dynamic clusters that exist in the main config Git repository but are
                missing from this instance.
              </Text>
              <Text fontSize='sm' mb={2}>
                Existing clusters are never overwritten. Only missing clusters are added.
              </Text>
              <Text fontSize='sm' color='gray.500'>
                This action is admin-only.
              </Text>
            </Box>
          )}

          {hasResult && (
            <Box>
              {RESULT_GROUPS.map((group) => {
                const items = [...(result[group.key] || [])].sort()
                return (
                  <Box key={group.key} mb={4}>
                    <Text fontSize='sm' fontWeight='bold' mb={1}>
                      {group.label} ({items.length})
                    </Text>
                    {items.length === 0 ? (
                      <Text fontSize='sm' color='gray.500'>None</Text>
                    ) : (
                      <List spacing={1}>
                        {items.map((name) => (
                          <ListItem key={name} fontSize='sm'>{name}</ListItem>
                        ))}
                      </List>
                    )}
                  </Box>
                )
              })}
              <Box mb={4}>
                <Text fontSize='sm' fontWeight='bold' mb={1}>
                  Errors ({errorEntries.length})
                </Text>
                {errorEntries.length === 0 ? (
                  <Text fontSize='sm' color='gray.500'>None</Text>
                ) : (
                  <List spacing={1}>
                    {errorEntries.map(([name, message]) => (
                      <ListItem key={name} fontSize='sm'>
                        <Text as='span' fontWeight='semibold'>{name}</Text>: {message}
                      </ListItem>
                    ))}
                  </List>
                )}
              </Box>
            </Box>
          )}

          {!hasResult && inlineError && (
            <NotFound text='Import could not be started.' />
          )}
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={handleClose} isDisabled={loading}>
            {hasResult ? 'Close' : 'Cancel'}
          </RMButton>
          {!hasResult && (
            <RMButton
              size='medium'
              onClick={handleImport}
              isLoading={loading}
              isDisabled={loading}>
              Import from Git
            </RMButton>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default DynamicClusterGitImportModal

DynamicClusterGitImportModal.propTypes = {
  isOpen: PropTypes.bool.isRequired,
  closeModal: PropTypes.func.isRequired
}
