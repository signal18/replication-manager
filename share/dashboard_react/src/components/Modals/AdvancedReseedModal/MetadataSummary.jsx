import React from 'react'
import { Box, HStack, Text, VStack } from '@chakra-ui/react'
import { formatLocalDateTime, formatMetadataTimestamp } from './helpers'

function MetadataSummary({ label, metadata, theme }) {
  if (!metadata) {
    return null
  }
  return (
    <Box borderWidth='1px' borderRadius='md' p={3} bg={theme === 'light' ? 'white' : 'gray.800'} width='100%'>
      <Text fontWeight='bold' fontSize='sm' mb={2}>
        {label}
      </Text>
      <VStack align='stretch' spacing={1} fontSize='sm'>
        <HStack>
          <Text fontWeight='semibold' minW='100px'>
            Start:
          </Text>
          <Text>{formatMetadataTimestamp(metadata, formatLocalDateTime) || 'N/A'}</Text>
        </HStack>
        {metadata.endTime && (
          <HStack>
            <Text fontWeight='semibold' minW='100px'>
              End:
            </Text>
            <Text>{formatLocalDateTime(metadata.endTime)}</Text>
          </HStack>
        )}
        {metadata.backupTool && (
          <HStack>
            <Text fontWeight='semibold' minW='100px'>
              Tool:
            </Text>
            <Text fontFamily='monospace'>{metadata.backupTool}</Text>
          </HStack>
        )}
        {metadata.backupSessionID && (
          <HStack align='flex-start'>
            <Text fontWeight='semibold' minW='100px'>
              Session:
            </Text>
            <Text fontSize='xs' fontFamily='monospace' wordBreak='break-all'>
              {metadata.backupSessionID}
            </Text>
          </HStack>
        )}
      </VStack>
    </Box>
  )
}

export default MetadataSummary
