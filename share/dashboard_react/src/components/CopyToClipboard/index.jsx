import { Box, Text, useDisclosure } from '@chakra-ui/react'
import React from 'react'
import { FaCopy } from 'react-icons/fa'
import { useDispatch } from 'react-redux'
import { showErrorToast, showSuccessToast } from '../../redux/toastSlice'
import RMIconButton from '../RMIconButton'
import styles from './styles.module.scss'
import RMButton from '../RMButton'
import CustomIcon from '../Icons/CustomIcon'

function CopyToClipboard({ text, textType = 'Text', copyIconPosition = 'center', className, keepOpen = false }) {
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

    document.body.removeChild(textArea)
  }
  return (
    <Box className={`${styles.container} ${className}`} onMouseEnter={handleMouseEnter} onMouseLeave={handleMouseLeave}>
      <span>{text}</span>
      {(isOpen || keepOpen) &&
        (keepOpen ? (
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
    </Box>
  )
}

export default CopyToClipboard
