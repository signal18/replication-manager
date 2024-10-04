import { Box, Text, useDisclosure } from '@chakra-ui/react'
import React from 'react'
import { FaCopy } from 'react-icons/fa'
import { useDispatch } from 'react-redux'
import { showErrorToast, showSuccessToast } from '../../redux/toastSlice'
import RMIconButton from '../RMIconButton'
import styles from './styles.module.scss'
import RMButton from '../RMButton'
import CustomIcon from '../Icons/CustomIcon'

function CopyToClipboard({ text, textType = 'Text', copyIconPosition = 'center', className, fromModal = false }) {
  const { isOpen, onOpen, onClose } = useDisclosure()
  const dispatch = useDispatch()

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
            title: `${textType} copied to clipboard`
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
    const element = fromModal ? document.querySelector("[class*='modalBody']") : document.body
    const textArea = document.createElement('textarea')
    textArea.value = textToCopy
    textArea.style.position = 'fixed'
    textArea.style.top = '-9999px' //  Move it off-screen
    textArea.style.left = '-9999px'
    textArea.style.width = '2em'
    textArea.style.height = '2em'
    textArea.style.padding = 0
    textArea.style.border = 'none'
    textArea.style.outline = 'none'
    textArea.style.boxShadow = 'none'
    textArea.style.background = 'transparent'
    element.appendChild(textArea)
    textArea.focus()
    textArea.select()
    try {
      document.execCommand('copy')
      dispatch(
        showSuccessToast({
          status: 'success',
          title: `${textType} copied to clipboard`
        })
      )
    } catch (err) {
      dispatch(
        showErrorToast({
          status: 'error',
          title: `Error while copying ${textType} to clipboard`
        })
      )
    }

    element.removeChild(textArea)
  }
  return (
    <Box className={`${styles.container} ${className}`} onMouseEnter={handleMouseEnter} onMouseLeave={handleMouseLeave}>
      {(isOpen || fromModal) &&
        (fromModal ? (
          <RMButton aria-label='Copy to clipboard' onClick={handleCopyClick} className={`${styles.btnCopy} `}>
            <CustomIcon icon={FaCopy} />
            Copy to clipboard
          </RMButton>
        ) : (
          <RMIconButton
            icon={FaCopy}
            onClick={handleCopyClick}
            className={`${styles.btnCopy} ${styles[copyIconPosition]}`}
            iconFontsize='1rem'
            aria-label='Copy to clipboard'
          />
        ))}
      <span className={'textToCopy'} dangerouslySetInnerHTML={{ __html: text }} />
    </Box>
  )
}

export default CopyToClipboard
