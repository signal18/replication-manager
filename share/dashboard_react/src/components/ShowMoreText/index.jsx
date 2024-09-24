import { Box } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import ConfirmModal from '../Modals/ConfirmModal'
import CopyToClipboard from '../CopyToClipboard'

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
      {isModalOpen && (
        <ConfirmModal
          isOpen={isModalOpen}
          closeModal={closeModal}
          title='Info'
          body={<CopyToClipboard text={text} className={styles.modalbodyText} keepOpen={true} />}
          showCancelButton={false}
          showConfirmButton={false}
        />
      )}
    </Box>
  )
}

export default ShowMoreText
