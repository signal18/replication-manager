import { Box } from '@chakra-ui/react'
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
  return [
    {
      key: 'Restic repository config',
      value: (
        <ResticRepositorySettings
          clusterName={selectedCluster?.name}
          config={selectedCluster?.config}
          user={user}
          dispatch={dispatch}
          onOpenInfoModal={onOpenInfoModal}
          isResticRepoConfigOpen={isResticRepoConfigOpen}
          onToggleResticRepoConfig={onToggleResticRepoConfig}
        />
      )
    },
    {
      key: 'Restic Purge Strategy',
      value: <ResticPurgeStrategy clusterName={selectedCluster?.name} config={selectedCluster?.config} />
    },
    {
      key: 'Restic Mount Settings',
      value: (
        <Box width='100%'>
          <ResticMountSettings
            clusterName={selectedCluster?.name}
            config={selectedCluster?.config}
            user={user}
          />
        </Box>
      )
    }
  ]
}

export default BackupSnapshotsSettings
