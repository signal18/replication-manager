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
  ModalOverlay,
  VStack
} from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import CopyToClipboard from '../../CopyToClipboard'

function CopyTextModal({ title, isOpen, closeModal, text, showPrettyJsonCheckbox }) {
  const [printPretty, setPrintPretty] = useState(false)
  const { theme } = useTheme()

  const handleSearch = () => {}

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent
        className={`${styles.modalContent} ${theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}`}>
        {title && <ModalHeader className={styles.modalHeader}>{title}</ModalHeader>}

        <ModalBody className={styles.modalBody}>
          {showPrettyJsonCheckbox && (
            <Flex className={styles.actions}>
              <Checkbox
                size='lg'
                isChecked={printPretty}
                onChange={(e) => setPrintPretty(e.target.checked)}
                className={styles.checkbox}>
                Print Pretty
              </Checkbox>
              {/* <HStack className={styles.search}>
                <label htmlFor='search'>Search</label>
                <Input id='search' type='search' onChange={handleSearch} />
              </HStack> */}
            </Flex>
          )}

          <CopyToClipboard
            text={printPretty ? `<pre>${JSON.stringify(JSON.parse(text), null, 2)}</pre>` : text}
            fromModal={true}
          />
        </ModalBody>
        <ModalCloseButton />
      </ModalContent>
    </Modal>
  )
}

export default CopyTextModal
