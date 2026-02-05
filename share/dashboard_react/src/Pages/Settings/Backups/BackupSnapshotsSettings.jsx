import { Box, Stack } from '@chakra-ui/react'
import React from 'react'
import ResticMountSettings from './ResticMountSettings'
import ResticPurgeStrategy from './ResticPurgeStrategy'
import ResticRepositorySettings from './ResticRepositorySettings'

function BackupSnapshotsSettings({
  selectedCluster,
  user,
  dispatch,
  onOpenInfoModal,
  isResticRepoConfigOpen,
  onToggleResticRepoConfig
}) {
  return (
    <Stack spacing={{ base: 3, lg: 4 }} width='100%'>
      <ResticRepositorySettings
        clusterName={selectedCluster?.name}
        config={selectedCluster?.config}
        user={user}
        dispatch={dispatch}
        onOpenInfoModal={onOpenInfoModal}
        isResticRepoConfigOpen={isResticRepoConfigOpen}
        onToggleResticRepoConfig={onToggleResticRepoConfig}
      />
      <ResticPurgeStrategy clusterName={selectedCluster?.name} config={selectedCluster?.config} />
      <Box width='100%'>
        <ResticMountSettings
          clusterName={selectedCluster?.name}
          config={selectedCluster?.config}
          user={user}
        />
      </Box>
    </Stack>
  )
}

export default BackupSnapshotsSettings
