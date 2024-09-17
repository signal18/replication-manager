import { Box, Tooltip } from '@chakra-ui/react'
import React from 'react'
import CheckOrCrossIcon from '../../../../components/Icons/CheckOrCrossIcon'
import CustomIcon from '../../../../components/Icons/CustomIcon'
import { HiExclamation } from 'react-icons/hi'

function ProxyStatus({ status }) {
  return (
    <Tooltip label={status}>
      <Box as='button'>
        {status === 'ProxyRunning' ? (
          <CheckOrCrossIcon isValid={true} variant='thumb' />
        ) : status === 'Failed' ? (
          <CheckOrCrossIcon isValid={false} variant='thumb' />
        ) : status === 'Suspect' ? (
          <CustomIcon icon={HiExclamation} color={'orange'} />
        ) : null}
      </Box>
    </Tooltip>
  )
}

export default ProxyStatus
