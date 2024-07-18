import React from 'react'
import CustomIcon from './CustomIcon'
import { HiCheck, HiThumbDown, HiThumbUp, HiX } from 'react-icons/hi'

function CheckOrCrossIcon({ isValid, isInvalid = true, variant = 'basic' }) {
  return isValid ? (
    <CustomIcon icon={variant === 'basic' ? HiCheck : HiThumbUp} color='green' />
  ) : isInvalid ? (
    <CustomIcon icon={variant === 'basic' ? HiX : HiThumbDown} color='red' />
  ) : null
}

export default CheckOrCrossIcon
