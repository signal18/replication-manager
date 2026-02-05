import { Box, Grid, GridItem, HStack, Stack, Text } from '@chakra-ui/react'
import React, { useMemo, useState } from 'react'
import { HiChevronDown, HiChevronUp } from 'react-icons/hi'
import Dropdown from '../../../components/Dropdown'
import NumberInput from '../../../components/NumberInput'
import RMSwitch from '../../../components/RMSwitch'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'
import styles from '../styles.module.scss'

function CompressionBufferSettings({ clusterName, config, user, sizeOptions, dispatch }) {
  const [isCompressionOpen, setIsCompressionOpen] = useState(true)
  const isReadOnly = user?.grants['cluster-settings'] == false
  const compressBackupsEnabled = Boolean(config?.compressBackups)
  const compressionBufferOptions = useMemo(
    () => [{ name: 'Use SST send buffer', value: 0 }, ...sizeOptions],
    [sizeOptions]
  )
  const compressBufferValue = config?.compressBackupsBufferSize ?? 0
  const compressBufferSelectedValue = compressBufferValue === 0 ? '0' : compressBufferValue

  const handleSettingChange = (setting, value) =>
    dispatch(
      setSetting({
        clusterName,
        setting,
        value
      })
    )

  const handleSwitchChange = (setting) =>
    dispatch(
      switchSetting({
        clusterName,
        setting
      })
    )

  return (
    <Box className={styles.panel} w='full'>
      <HStack
        as='button'
        type='button'
        spacing={2}
        onClick={() => setIsCompressionOpen((prev) => !prev)}
        aria-expanded={isCompressionOpen}
        aria-controls='compression-settings-content'
        className={styles.panelHeader}
      >
        <Stack spacing={1} className={styles.panelHeaderContent}>
          <Text className={styles.panelTitle}>Compression & buffers</Text>
          <Text className={styles.panelDescription}>
            Tune pgzip compression and buffer sizes for backups and SST streaming.
          </Text>
        </Stack>
        <Box className={styles.panelChevron}>{isCompressionOpen ? <HiChevronUp /> : <HiChevronDown />}</Box>
      </HStack>
      <Box id='compression-settings-content' className={styles.panelBody} display={isCompressionOpen ? 'block' : 'none'}>
        <Stack spacing={{ base: 1, md: 2 }}>
          <Grid
            className={styles.resticMountGrid}
            templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={1}
            w='full'
          >
            <GridItem className={styles.rowLabel}>
              <Text>Use compression</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={compressBackupsEnabled}
                isDisabled={isReadOnly}
                confirmTitle={'Confirm switch settings for compress-backups?'}
                onChange={() => handleSwitchChange('compress-backups')}
              />
              <Text className={styles.helperText}>
                Enables pgzip compression for backup files and compressed reseed streams.
              </Text>
            </GridItem>
          </Grid>

          <Grid
            className={styles.resticMountGrid}
            templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={1}
            w='full'
          >
            <GridItem className={styles.rowLabel}>
              <Text>Reseed decompress on target</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={config?.backupReseedRemoteDecompress}
                isDisabled={isReadOnly}
                confirmTitle={'Confirm switch settings for backup-reseed-remote-decompress?'}
                onChange={() => handleSwitchChange('backup-reseed-remote-decompress')}
              />
              <Text className={styles.helperText}>
                Sends a compressed stream and decompresses on the target node to save network bandwidth.
              </Text>
            </GridItem>
          </Grid>

          {compressBackupsEnabled && (
            <>
              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Compression level</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <NumberInput
                    min={1}
                    max={9}
                    value={config?.compressBackupsCompressionLevel}
                    showEditButton={true}
                    showConfirmModal={true}
                    isDisabled={isReadOnly}
                    confirmTitle={'Confirm change compression level to: '}
                    onConfirm={(value) => handleSettingChange('compress-backups-compression-level', value)}
                  />
                  <Text className={styles.helperText}>1 is fastest, 9 gives the smallest backups.</Text>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Parallel blocks</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <NumberInput
                    min={1}
                    max={32}
                    value={config?.compressBackupsParallelBlocks}
                    showEditButton={true}
                    showConfirmModal={true}
                    isDisabled={isReadOnly}
                    confirmTitle={'Confirm change parallel blocks to: '}
                    onConfirm={(value) => handleSettingChange('compress-backups-parallel-blocks', value)}
                  />
                  <Text className={styles.helperText}>
                    Higher values speed up decompression at the cost of CPU and memory.
                  </Text>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Compression buffer size</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <Dropdown
                    options={compressionBufferOptions}
                    selectedValue={compressBufferSelectedValue}
                    confirmTitle={`Confirm change 'compress-backups-buffer-size' to `}
                    isDisabled={isReadOnly}
                    onChange={(size) => handleSettingChange('compress-backups-buffer-size', size)}
                  />
                  <Text className={styles.helperText}>
                    Buffer for pgzip readers during backup/restore decompression. Set to 0 to reuse the SST send buffer.
                  </Text>
                </GridItem>
              </Grid>
            </>
          )}

          <Grid
            className={styles.resticMountGrid}
            templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={1}
            w='full'
          >
            <GridItem className={styles.rowLabel}>
              <Text>SST send buffer (network)</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <Dropdown
                options={sizeOptions}
                selectedValue={config?.sstSendBuffer}
                confirmTitle={`Confirm change 'sst-send-buffer' to `}
                isDisabled={isReadOnly}
                onChange={(size) => handleSettingChange('sst-send-buffer', size)}
              />
              <Text className={styles.helperText}>
                Buffer used for SST streaming over the network. Also acts as the fallback for decompression when the
                compression buffer is 0.
              </Text>
            </GridItem>
          </Grid>
        </Stack>
      </Box>
    </Box>
  )
}

export default CompressionBufferSettings
