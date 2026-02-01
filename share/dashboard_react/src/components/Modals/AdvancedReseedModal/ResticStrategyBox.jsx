import React from 'react'
import { Box, Checkbox, FormControl, FormLabel, Input, Select, Text, VStack } from '@chakra-ui/react'

function ResticStrategyBox({
  theme,
  resticStrategy,
  setResticStrategy,
  resticCleanup,
  setResticCleanup,
  resticUseTempDir,
  setResticUseTempDir,
  resticTempDir,
  setResticTempDir,
  resticTempDirPlaceholder,
  dumpAllowed
}) {
  return (
    <Box borderWidth='1px' borderRadius='md' p={3} bg={theme === 'light' ? 'gray.50' : 'gray.700'} mb={3}>
      <VStack align='stretch' spacing={3}>
        <FormControl>
          <FormLabel fontWeight='bold' fontSize='sm'>
            Restic Strategy
          </FormLabel>
          <Select value={resticStrategy} onChange={(e) => setResticStrategy(e.target.value)}>
            <option value='auto'>Auto (restore/mount/dump)</option>
            <option value='restore'>Restore (extract to disk)</option>
            <option value='mount'>Mount (FUSE)</option>
            {dumpAllowed && <option value='dump'>Dump (stream)</option>}
          </Select>
          <Text fontSize='xs' color={theme === 'light' ? 'gray.600' : 'gray.300'} mt={1}>
            Auto chooses restore or mount by backup type, and uses dump for single-file logical backups when FUSE is
            unavailable.
          </Text>
        </FormControl>
        {resticStrategy === 'restore' && (
          <FormControl>
            <Checkbox isChecked={resticUseTempDir} onChange={(e) => setResticUseTempDir(e.target.checked)}>
              Use temporary directory
            </Checkbox>
            {!resticUseTempDir && (
              <Text fontSize='xs' color={theme === 'light' ? 'orange.600' : 'orange.300'} mt={1}>
                Turning this off restores directly into the backup directory and will overwrite the backup server or
                master latest backup.
              </Text>
            )}
          </FormControl>
        )}
        {resticStrategy === 'restore' && resticUseTempDir && (
          <FormControl>
            <FormLabel fontWeight='bold' fontSize='sm'>
              Temporary Directory (optional)
            </FormLabel>
            <Input
              value={resticTempDir}
              onChange={(e) => setResticTempDir(e.target.value)}
              placeholder={resticTempDirPlaceholder}
            />
            <Text fontSize='xs' color={theme === 'light' ? 'gray.600' : 'gray.300'} mt={1}>
              Leave empty to use server default.
            </Text>
          </FormControl>
        )}
        {resticStrategy === 'restore' && (
          <FormControl>
            <Checkbox
              isChecked={resticCleanup}
              onChange={(e) => setResticCleanup(e.target.checked)}
              isDisabled={!resticUseTempDir}
            >
              Cleanup temporary files after reseed
            </Checkbox>
          </FormControl>
        )}
      </VStack>
    </Box>
  )
}

export default ResticStrategyBox
