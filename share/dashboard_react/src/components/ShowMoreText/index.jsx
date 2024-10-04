import { Box } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import CopyTextModal from '../Modals/CopyTextModal'

function ShowMoreText({ text, maxLength = 30 }) {
  const [truncatedText, setTruncatedText] = useState('')
  const [isModalOpen, setIsModalOpen] = useState(false)
  useEffect(() => {
    if (text) {
      setTruncatedText(text.slice(0, maxLength))
    }
  }, [text])

  const openModal = () => {
    setIsModalOpen(true)
  }

  const closeModal = () => {
    setIsModalOpen(false)
  }

  return (
    <Box>
      <span>{text.length > maxLength ? `${truncatedText}...` : text}</span>
      {text.length > maxLength && (
        <button onClick={openModal} className={styles.showmore}>
          more
        </button>
      )}
      {isModalOpen && <CopyTextModal isOpen={isModalOpen} closeModal={closeModal} title='Info' text={text} />}
    </Box>
  )
}

export default ShowMoreText
