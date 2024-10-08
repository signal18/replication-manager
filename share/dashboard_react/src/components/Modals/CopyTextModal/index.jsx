import {
  Box,
  Checkbox,
  Flex,
  HStack,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalHeader,
  ModalOverlay
} from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import CopyToClipboard from '../../CopyToClipboard'
import CopyObjectText from '../../CopyObjectText'

function CopyTextModal({ title, isOpen, closeModal, text, showPrettyJsonCheckbox }) {
  const [printPretty, setPrintPretty] = useState(false)
  const { theme } = useTheme()

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent
        className={`${styles.modalContent} ${theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}`}>
        {title && <ModalHeader className={styles.modalHeader}>{title}</ModalHeader>}

        <ModalBody className={styles.modalBody}>
          <CopyObjectText showPrettyJsonCheckbox={showPrettyJsonCheckbox} text={text} fromModal={true} />
        </ModalBody>
        <ModalCloseButton />
      </ModalContent>
    </Modal>
  )
}

export default CopyTextModal
