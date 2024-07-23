import { Box, IconButton, useDisclosure } from '@chakra-ui/react'
import React from 'react'
import { FaCopy } from 'react-icons/fa'
import { useDispatch } from 'react-redux'
import { showErrorBanner, showSuccessBanner } from '../utility/common'
import { showErrorToast, showSuccessToast } from '../redux/toastSlice'

function GTID({ text, copyIconPosition = 'center' }) {
  const { isOpen, onOpen, onClose } = useDisclosure()
  const dispatch = useDispatch()

  const styles = {
    btnCopy: {
      position: 'absolute',
      top: '50%',
      right: copyIconPosition === 'center' ? '25%' : '0',
      ...(copyIconPosition === 'center' ? { left: '25%' } : {}),
      transform: 'translateY(-50%)',
      background: 'rgba(26,36,42,.5)',
      _hover: {
        background: 'rgba(26,36,42,.5)'
      }
    }
  }

  const handleMouseEnter = () => {
    onOpen()
  }

  const handleMouseLeave = () => {
    onClose()
  }

  const handleCopyClick = async () => {
    if (navigator.clipboard) {
      try {
        await navigator.clipboard.writeText(text)
        dispatch(
          showSuccessToast({
            status: 'success',
            title: 'GTID copied to clipboard'
          })
        )
      } catch (err) {
        fallbackCopyTextToClipboard(text)
      }
    } else {
      fallbackCopyTextToClipboard(text)
    }
  }

  const fallbackCopyTextToClipboard = (textToCopy) => {
    const textArea = document.createElement('textarea')
    textArea.value = textToCopy
    textArea.style.position = 'fixed'
    textArea.style.top = 0
    textArea.style.left = 0
    textArea.style.width = '2em'
    textArea.style.height = '2em'
    textArea.style.padding = 0
    textArea.style.border = 'none'
    textArea.style.outline = 'none'
    textArea.style.boxShadow = 'none'
    textArea.style.background = 'transparent'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()

    try {
      document.execCommand('copy')
      dispatch(
        showSuccessToast({
          status: 'success',
          title: 'GTID copied to clipboard'
        })
      )
    } catch (err) {
      dispatch(
        showErrorToast({
          status: 'error',
          title: 'Error while copying GTID to clipboard'
        })
      )
    }

    document.body.removeChild(textArea)
  }
  return (
    <Box
      fontSize={'16px'}
      whiteSpace='pre'
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
      position='relative'>
      {text}
      {isOpen && (
        <IconButton
          icon={<FaCopy />}
          onClick={handleCopyClick}
          sx={styles.btnCopy}
          aria-label='Copy to clipboard'
          size='sm'
        />
      )}
    </Box>
  )
}

export default GTID
