import { Box, Checkbox, Flex, VStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import CopyToClipboard from '../CopyToClipboard'
import styles from './styles.module.scss'

function CopyObjectText({ text, showPrettyJsonCheckbox = true, fromModal = false }) {
  const [printPretty, setPrintPretty] = useState(true)
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
        text={printPretty ? JSON.stringify(JSON.parse(text), null, 2) : text}
        fromModal={fromModal}
        keepOpen={true}
        printPretty={printPretty}
      />
    </VStack>
  )
}

export default CopyObjectText
