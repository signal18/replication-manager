import React from 'react'
import { Badge, Box, HStack, Text, VStack, Wrap, WrapItem } from '@chakra-ui/react'
import { formatEpochDateTime } from './helpers'
import MetadataSummary from './MetadataSummary'

function SnapshotDetailsPanel({
  theme,
  snapshot,
  statusBadge,
  snapshotTags,
  createdTime,
  metadataReady,
  metadataStatus,
  metadataError,
  backupTool,
  pathInfo,
  resolvedMethod,
  logicalMetadata,
  physicalMetadata,
  encryptionMode,
  encryptionModeLabel
}) {
  const encryptionColorScheme =
    encryptionMode === 'stream-encrypted' ? 'blue' : encryptionMode === 'legacy-encrypted' ? 'orange' : 'gray'

  return (
    <Box borderWidth='1px' borderRadius='md' p={4} bg={theme === 'light' ? 'gray.50' : 'gray.700'}>
      <Text fontWeight='bold' mb={3} fontSize='sm' color={theme === 'light' ? 'gray.700' : 'gray.200'}>
        Snapshot Details
      </Text>
      <VStack align='stretch' spacing={2}>
        <HStack>
          <Text fontWeight='semibold' fontSize='sm' minW='130px'>
            Hostname:
          </Text>
          <Text fontSize='sm' fontFamily='monospace'>
            {snapshot.hostname || 'N/A'}
          </Text>
        </HStack>

        <HStack align='flex-start'>
          <Text fontWeight='semibold' fontSize='sm' minW='130px'>
            Snapshot ID:
          </Text>
          <Text fontSize='xs' fontFamily='monospace' wordBreak='break-all'>
            {snapshot.id}
          </Text>
        </HStack>

        <HStack>
          <Text fontWeight='semibold' fontSize='sm' minW='130px'>
            Created:
          </Text>
          <Text fontSize='sm'>{createdTime || 'N/A'}</Text>
        </HStack>

        {statusBadge && (
          <HStack>
            <Text fontWeight='semibold' fontSize='sm' minW='130px'>
              Status:
            </Text>
            {statusBadge}
            {snapshotTags?.isLatestView && (
              <Badge colorScheme='green' variant='outline' fontSize='0.6rem' ml={1}>
                latest
              </Badge>
            )}
          </HStack>
        )}

        {snapshotTags?.sessionId && (
          <HStack align='flex-start'>
            <Text fontWeight='semibold' fontSize='sm' minW='130px'>
              Session ID:
            </Text>
            <Text fontSize='xs' fontFamily='monospace' wordBreak='break-all'>
              {snapshotTags.sessionId}
            </Text>
          </HStack>
        )}

        <HStack>
          <Text fontWeight='semibold' fontSize='sm' minW='130px'>
            Metadata:
          </Text>
          <Badge colorScheme={metadataReady ? 'green' : 'orange'} fontSize='xs'>
            {metadataStatus}
          </Badge>
        </HStack>
        {metadataError && (
          <Text fontSize='xs' color='red.400' pl={metadataReady ? 0 : 0}>
            {metadataError}
          </Text>
        )}

        {backupTool && (
          <HStack>
            <Text fontWeight='semibold' fontSize='sm' minW='130px'>
              Backup Tool:
            </Text>
            <HStack spacing={2}>
              <Text fontSize='sm' fontFamily='monospace'>
                {backupTool}
              </Text>
              {pathInfo?.isAdhoc && (
                <Badge colorScheme='purple' fontSize='xs'>
                  adhoc
                </Badge>
              )}
            </HStack>
          </HStack>
        )}

        {resolvedMethod && (
          <HStack>
            <Text fontWeight='semibold' fontSize='sm' minW='130px'>
              Method:
            </Text>
            <Text fontSize='sm' fontFamily='monospace'>
              {resolvedMethod}
            </Text>
          </HStack>
        )}

        {encryptionModeLabel && (
          <HStack>
            <Text fontWeight='semibold' fontSize='sm' minW='130px'>
              Encryption Mode:
            </Text>
            <Badge colorScheme={encryptionColorScheme} variant='subtle' fontSize='0.65rem'>
              {encryptionModeLabel}
            </Badge>
          </HStack>
        )}

        {pathInfo?.epoch && (
          <HStack>
            <Text fontWeight='semibold' fontSize='sm' minW='130px'>
              Backup Timestamp:
            </Text>
            <Text fontSize='sm'>{formatEpochDateTime(pathInfo.epoch)}</Text>
          </HStack>
        )}

        {(logicalMetadata || physicalMetadata) && (
          <VStack align='stretch' spacing={2} mt={2}>
            <MetadataSummary label='Logical backup' metadata={logicalMetadata} theme={theme} />
            <MetadataSummary label='Physical backup' metadata={physicalMetadata} theme={theme} />
          </VStack>
        )}

        {snapshot.tags && snapshot.tags.length > 0 && (
          <HStack align='flex-start'>
            <Text fontWeight='semibold' fontSize='sm' minW='130px'>
              Tags:
            </Text>
            <Wrap>
              {snapshot.tags.map((tag, index) => (
                <WrapItem key={index}>
                  <Badge colorScheme='blue' fontSize='xs'>
                    {tag}
                  </Badge>
                </WrapItem>
              ))}
            </Wrap>
          </HStack>
        )}

        {snapshot.paths && snapshot.paths.length > 0 && (
          <VStack align='stretch' spacing={1}>
            <Text fontWeight='semibold' fontSize='sm'>
              Paths:
            </Text>
            <VStack align='stretch' spacing={1} pl={4}>
              {snapshot.paths.map((path, index) => (
                <Text
                  key={index}
                  fontSize='xs'
                  fontFamily='monospace'
                  color={theme === 'light' ? 'gray.600' : 'gray.400'}
                >
                  • {path}
                </Text>
              ))}
            </VStack>
          </VStack>
        )}
      </VStack>
    </Box>
  )
}

export default SnapshotDetailsPanel
