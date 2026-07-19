import React from 'react'
import { useSelector } from 'react-redux'
import { Box, HStack, Text } from '@chakra-ui/react'
import { HiExclamation } from 'react-icons/hi'

// StandbyBanner — a persistent, hard-to-miss warning shown ONLY when this
// replication-manager is a Standby ("S"). The config-save gate is active-gated
// (server.go), so a standby never writes default.toml: any setting changed here
// lives in memory only, is lost on restart, and is overwritten when the standby
// syncs from the active instance. Self-gates on redux monitor.status, so it can be
// dropped at the top of any settings page (global or cluster level) with no props.
function StandbyBanner() {
  const monitor = useSelector((state) => state.globalClusters.monitor)
  if (monitor?.status !== 'S') return null
  return (
    <Box
      role='alert'
      borderWidth='1px'
      borderColor='red.500'
      bg='rgba(229,62,62,0.12)'
      borderRadius='md'
      px={4}
      py={3}
      mb={3}
    >
      <HStack align='start' spacing={3}>
        <Box as={HiExclamation} color='red.400' boxSize='1.5rem' flexShrink={0} mt='1px' />
        <Text fontSize='sm' color='red.400' fontWeight='bold'>
          Standby (DR) instance — changes made here are applied in memory only and are lost on
          restart. Set server settings on the active instance to make them persist.
        </Text>
      </HStack>
    </Box>
  )
}

export default StandbyBanner
