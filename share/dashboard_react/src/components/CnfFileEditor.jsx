import { Box, Button, VStack, HStack, Textarea, Text, useToast, Spinner, Alert, AlertIcon, Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton, useDisclosure } from '@chakra-ui/react'
import { useState } from 'react'
import { TbDownload, TbDeviceFloppy, TbRefresh } from 'react-icons/tb'
import { useDispatch } from 'react-redux'
import ConfirmModal from './Modals/ConfirmModal'
import PropTypes from 'prop-types'

/**
 * Generic CNF file editor component that can be used for different configuration file types
 * 
 * @param {Object} props
 * @param {string} props.clusterName - Name of the cluster
 * @param {Object} props.user - User object with grants
 * @param {string} props.className - Additional CSS classes
 * @param {string} props.fileType - Type of file being edited (e.g., 'MySQL Defaults', 'Preserved Variables')
 * @param {string} props.fileName - Name of the configuration file (e.g., 'mysql_defaults.cnf', 'preserved_variables.cnf')
 * @param {Function} props.loadAction - Redux action to load the file content
 * @param {Function} props.saveAction - Redux action to save the file content
 * @param {string} props.placeholder - Placeholder text for the textarea
 * @param {Object} props.infoContent - Content for the information modal (optional)
 * @param {React.ReactNode} props.warningAlert - Warning alert content to show before loading
 * @param {React.ReactNode} props.scopeInfo - Scope information to show below the editor
 * @param {React.ReactNode} props.confirmModalContent - Content for the save confirmation modal
 */
function CnfFileEditor({ 
  clusterName, 
  user, 
  className,
  fileType,
  fileName,
  loadAction,
  saveAction,
  placeholder,
  infoContent,
  warningAlert,
  scopeInfo,
  confirmModalContent
}) {
  const [content, setContent] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [isLoaded, setIsLoaded] = useState(false)
  const [isSaving, setIsSaving] = useState(false)
  const [hasChanges, setHasChanges] = useState(false)
  const [originalContent, setOriginalContent] = useState('')
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const { isOpen: isInfoOpen, onOpen: onInfoOpen, onClose: onInfoClose } = useDisclosure()
  
  const dispatch = useDispatch()
  const toast = useToast()

  const handleLoadContent = async () => {
    setIsLoading(true)
    try {
      const result = await dispatch(loadAction({ clusterName })).unwrap()
      setContent(result.content || '')
      setOriginalContent(result.content || '')
      setIsLoaded(true)
      setHasChanges(false)
      toast({
        title: 'Success',
        description: `${fileType} configuration loaded successfully`,
        status: 'success',
        duration: 3000,
        isClosable: true
      })
    } catch (error) {
      toast({
        title: 'Error',
        description: error.message || `Failed to load ${fileType} configuration`,
        status: 'error',
        duration: 5000,
        isClosable: true
      })
    } finally {
      setIsLoading(false)
    }
  }

  const handleContentChange = (e) => {
    setContent(e.target.value)
    setHasChanges(e.target.value !== originalContent)
  }

  const handleSave = async () => {
    setIsSaving(true)
    try {
      await dispatch(saveAction({ clusterName, content })).unwrap()
      setOriginalContent(content)
      setHasChanges(false)
      setIsConfirmModalOpen(false)
      toast({
        title: 'Success',
        description: `${fileType} configuration saved and reloaded successfully`,
        status: 'success',
        duration: 3000,
        isClosable: true
      })
    } catch (error) {
      toast({
        title: 'Error',
        description: error.message || `Failed to save ${fileType} configuration`,
        status: 'error',
        duration: 5000,
        isClosable: true
      })
    } finally {
      setIsSaving(false)
    }
  }

  const handleReset = () => {
    setContent(originalContent)
    setHasChanges(false)
  }

  const handleSaveClick = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const isDisabled = user?.grants['cluster-settings'] === false

  return (
    <VStack spacing={4} align="stretch" className={className}>
      {!isLoaded ? (
        <VStack spacing={3} align="stretch">
          {warningAlert && (
            <Alert status="warning" borderRadius="md" size="sm">
              <AlertIcon />
              <Box fontSize="sm">
                {warningAlert}
              </Box>
            </Alert>
          )}
          <Button
            leftIcon={isLoading ? <Spinner size="sm" /> : <TbDownload />}
            colorScheme="blue"
            onClick={handleLoadContent}
            isLoading={isLoading}
            isDisabled={isDisabled || isLoading}
          >
            Load {fileType} Configuration
          </Button>
        </VStack>
      ) : (
        <>
          <HStack spacing={2} justify="space-between">
            <HStack spacing={2}>
              <Button
                leftIcon={<TbDeviceFloppy />}
                colorScheme="green"
                onClick={handleSaveClick}
                isLoading={isSaving}
                isDisabled={isDisabled || !hasChanges || isSaving}
              >
                Save Changes
              </Button>
              <Button
                leftIcon={<TbRefresh />}
                colorScheme="orange"
                onClick={handleReset}
                isDisabled={isDisabled || !hasChanges || isSaving}
              >
                Reset
              </Button>
              <Button
                leftIcon={<TbDownload />}
                colorScheme="blue"
                onClick={handleLoadContent}
                isLoading={isLoading}
                isDisabled={isDisabled || isLoading}
                variant="outline"
              >
                Reload
              </Button>
            </HStack>
            {hasChanges && (
              <Text color="orange.500" fontSize="sm" fontWeight="bold">
                Unsaved changes
              </Text>
            )}
          </HStack>

          <Box>
            <Textarea
              value={content}
              onChange={handleContentChange}
              isDisabled={isDisabled || isSaving}
              fontFamily="monospace"
              fontSize="sm"
              minHeight="500px"
              placeholder={placeholder || `${fileType} configuration content...`}
              resize="vertical"
            />
          </Box>

          {scopeInfo && (
            <Text fontSize="xs" color="gray.500">
              {scopeInfo}
            </Text>
          )}
        </>
      )}

      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={`Save ${fileType} Configuration?`}
          onConfirmClick={handleSave}
        >
          {confirmModalContent || (
            <VStack align="start" spacing={2}>
              <Text>
                This will save the {fileType} configuration and automatically reload it.
              </Text>
              <Text fontWeight="bold" mt={2}>
                Are you sure you want to continue?
              </Text>
            </VStack>
          )}
        </ConfirmModal>
      )}

      {/* Information Modal */}
      {infoContent && (
        <Modal isOpen={isInfoOpen} onClose={onInfoClose} size="xl">
          <ModalOverlay />
          <ModalContent>
            <ModalHeader>{fileType} Configuration Information</ModalHeader>
            <ModalCloseButton />
            <ModalBody pb={6}>
              {infoContent}
            </ModalBody>
          </ModalContent>
        </Modal>
      )}
    </VStack>
  )
}

CnfFileEditor.propTypes = {
  clusterName: PropTypes.string.isRequired,
  user: PropTypes.object,
  className: PropTypes.string,
  fileType: PropTypes.string.isRequired,
  fileName: PropTypes.string.isRequired,
  loadAction: PropTypes.func.isRequired,
  saveAction: PropTypes.func.isRequired,
  placeholder: PropTypes.string,
  infoContent: PropTypes.node,
  warningAlert: PropTypes.node,
  scopeInfo: PropTypes.node,
  confirmModalContent: PropTypes.node
}

export default CnfFileEditor
