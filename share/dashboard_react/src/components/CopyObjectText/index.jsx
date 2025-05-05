import { Box, Checkbox, Flex, VStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import CopyToClipboard from '../CopyToClipboard'
import styles from './styles.module.scss'

function CopyObjectText({ text, showPrettyJsonCheckbox = true, fromModal = false }) {
  const [printPretty, setPrintPretty] = useState(true)

  // check if the text is a valid JSON
  const isValidJson = (str) => {
    try {
      JSON.parse(str)
      return true
    } catch (e) {
      return false
    }
  }
  
  const sanitizedText = isValidJson(text) ? JSON.stringify(JSON.parse(text), null, printPretty ? 2 : 0) : text

  return (
    <VStack className={styles.copyContainer}>
      {showPrettyJsonCheckbox && (
        <Flex className={styles.actions}>
          <Checkbox
            size='lg'
            isChecked={printPretty}
            onChange={(e) => setPrintPretty(e.target.checked)}
            className={styles.checkbox}>
            Print Pretty
          </Checkbox>
        </Flex>
      )}

      <CopyToClipboard
        text={sanitizedText}
        fromModal={fromModal}
        keepOpen={true}
        printPretty={printPretty}
      />
    </VStack>
  )
}

export default CopyObjectText
