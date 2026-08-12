import { Badge, Box, Flex, Progress, Text } from '@chakra-ui/react'
import React from 'react'
import CommonModal from '../CommonModal'
import {
  getActiveReseeds,
  reseedHasBar,
  reseedHasBytes,
  formatBytes,
  formatElapsed,
  formatRate,
} from '../../../utility/reseedProgress'

// ReseedProgressModal shows every server currently being reseeded/rejoined with a
// live progress bar (byte-instrumented methods) or an indeterminate timer + the
// generic "started T" line (methods without byte counting). The panel landing each
// reseed's true outcome at completion is the visible proof the rejoin reconcile works.
function ReseedProgressModal({ isOpen, closeModal, servers }) {
  const active = getActiveReseeds(servers)

  const body = (
    <Flex direction='column' gap={4}>
      {active.length === 0 && <Text>No reseed in progress.</Text>}
      {active.map((rp) => {
        const hasBar = reseedHasBar(rp)
        const hasBytes = reseedHasBytes(rp)
        return (
          <Box key={rp.url} borderWidth='1px' borderRadius='md' p={3}>
            <Flex justify='space-between' align='center' mb={2}>
              <Text fontWeight='bold'>{rp.url}</Text>
              <Flex gap={2}>
                {rp.fromRejoin && <Badge colorScheme='purple'>rejoin</Badge>}
                {rp.task && <Badge colorScheme='blue'>{rp.task}</Badge>}
              </Flex>
            </Flex>

            {hasBar ? (
              <>
                <Progress
                  value={rp.percent}
                  size='sm'
                  colorScheme='blue'
                  hasStripe
                  isAnimated
                  borderRadius='md'
                />
                <Text fontSize='sm' mt={1}>
                  {formatBytes(rp.bytes)} / {formatBytes(rp.total)} ({rp.percent}%)
                  {` · ${formatRate(rp.rateBytesSec, rp.elapsedSecs)}`}
                  {` · ${formatElapsed(rp.elapsedSecs)}`}
                </Text>
              </>
            ) : (
              <>
                <Progress size='sm' colorScheme='blue' isIndeterminate borderRadius='md' />
                <Text fontSize='sm' mt={1}>
                  {hasBytes ? (
                    <>
                      {formatBytes(rp.bytes)} streamed
                      {` · ${formatRate(rp.rateBytesSec, rp.elapsedSecs)}`}
                      {` · ${formatElapsed(rp.elapsedSecs)}`}
                    </>
                  ) : (
                    rp.line || `in progress · ${formatElapsed(rp.elapsedSecs)}`
                  )}
                </Text>
              </>
            )}

            {rp.backup && (
              <Text fontSize='xs' opacity={0.7} mt={1}>
                backup: {rp.backup}
              </Text>
            )}
          </Box>
        )
      })}
    </Flex>
  )

  return (
    <CommonModal
      isOpen={isOpen}
      closeModal={closeModal}
      title='Reseed / rejoin in progress'
      size='lg'
      body={body}
    />
  )
}

export default ReseedProgressModal
