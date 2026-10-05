import { Box, Text, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../../../components/Dropdown'
import TableType2 from '../../../components/TableType2'
import parentStyles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import { convertObjectToArrayForDropdown } from '../../../utility/common'
import TextForm from '../../../components/TextForm'
import RMIconButton from '../../../components/RMIconButton'
import CommonModal from '../../../components/Modals/CommonModal'
import modalStyles from '../../../components/Modals/styles.module.scss'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function OrchestratorDisks({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const [helpAction, setHelpAction] = useState({ title: '', body: <></> })
  const [isHelpModalOpen, setIsHelpModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setHelpAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsHelpModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      onClick={() => openInfoModal(title, content)}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
    />
  )

  const hRunAs = `**Database Run As UID[:GID]**\n\n*Advanced setting.* Leave it empty unless the image or the storage requires a specific user.\n\nThe numeric user the database container process runs as: OpenSVC \`--user\`, Kubernetes \`securityContext\` (\`runAsUser\`/\`runAsGroup\`).\n\n| Value | Meaning |\n|---|---|\n| empty | legacy behavior: \`--user mysql\` for MySQL images, the image's own user otherwise |\n| \`1001\` | UID 1001, the GID defaults to the UID |\n| \`1001:999\` | UID 1001, GID 999 |\n| \`0\` | root, taken literally |\n\nIndependent of the data volume owner (**Database Volume UID[:GID]**): the two must match for the database to write its data. Root works for the official MariaDB and MySQL images, which drop to 999:999 themselves; Percona Server refuses to run as root.\n\nA change only marks the database services for reprovisioning; it does not change a running container.\n\nConfig: \`prov-db-run-as-uid\``

  const hVolume = `**Database Volume UID[:GID]**\n\n*Advanced setting.* Leave it empty unless the image or the storage requires a specific owner.\n\nThe numeric owner of the database data volume, applied by the OpenSVC bootstrap chown and by the Kubernetes init container.\n\n| Value | Meaning |\n|---|---|\n| empty | legacy owner \`999:999\` (nothing on Kubernetes), except Percona Server images: \`1001:1001\` |\n| \`1001\` | UID 1001, the GID defaults to the UID |\n| \`1001:999\` | UID 1001, GID 999 |\n| \`0\` | root, taken literally |\n\nIndependent of the user the process runs as (**Database Run As UID[:GID]**). Percona Server images run as 1001, so a 999 volume cannot be written.\n\nA change only marks the database services for reprovisioning; it does not change the owner of an existing volume.\n\nConfig: \`prov-db-volume-uid\``
  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const [serviceDisks, setServiceDisks] = useState([])
  const [serviceFS, setServiceFS] = useState([])
  const [servicePool, setServicePool] = useState([])
  const orchestrator = selectedCluster?.config?.provOrchestrator
  const showRunAs = orchestrator === 'opensvc' || orchestrator === 'kube'
  // Empty owner = legacy 999:999, except Percona Server images: 1001. Empty run as = legacy.
  const chownPlaceholder = (selectedCluster?.config?.provDbDockerImg || '').toLowerCase().includes('percona') ? '1001 (Percona image)' : 'legacy (999:999)'
  const settingsDisabled = user?.grants['cluster-settings'] === false

  useEffect(() => {
    if (monitor?.serviceDisk) {
      setServiceDisks(convertObjectToArrayForDropdown(monitor.serviceDisk))
    }
    if (monitor?.serviceFS) {
      setServiceFS(convertObjectToArrayForDropdown(monitor.serviceFS))
    }
    if (monitor?.servicePool) {
      setServicePool(convertObjectToArrayForDropdown(monitor.servicePool))
    }
  }, [monitor?.serviceDisk, monitor?.serviceFS, monitor?.servicePool])

  const dataObject = [
    {
      key: 'Database',
      value: [
        {
          key: 'Database Disk Type',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={serviceDisks}
              selectedValue={selectedCluster?.config?.provDbDiskType}
              confirmTitle={`Confirm change DB disk type to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-db-disk-type',
                    value: value
                  })
                )
              }}
            />
          )
        },
        ...(showRunAs
          ? [
              {
                key: 'Database Run As UID[:GID]',
                help: h(hRunAs, 'Database Run As UID[:GID]'),
                value: (
                  <TextForm
                    value={selectedCluster?.config?.provDbRunAsUid ?? ''}
                    placeholder={'legacy (image user)'}
                    regexPattern='^[0-9]{0,10}(:[0-9]{0,10})?$'
                    maxLength={21}
                    isDisabled={settingsDisabled}
                    className={parentStyles.textbox}
                    confirmTitle='Confirm database run as UID[:GID]'
                    confirmBody='Advanced setting. UID or UID:GID the database container process runs as (GID defaults to the UID, 0 = root). Empty = legacy behavior (mysql user for MySQL images, the image user otherwise). Independent of the data volume owner. This only marks database services for reprovisioning; it does not change a running container. Set run as UID[:GID] to: '
                    onSave={(value) =>
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-db-run-as-uid',
                          value: value
                        })
                      )
                    }
                  />
                )
              },
              {
                key: 'Database Volume UID[:GID]',
                help: h(hVolume, 'Database Volume UID[:GID]'),
                value: (
                  <TextForm
                    value={selectedCluster?.config?.provDbVolumeUid ?? ''}
                    placeholder={chownPlaceholder}
                    regexPattern='^[0-9]{0,10}(:[0-9]{0,10})?$'
                    maxLength={21}
                    isDisabled={settingsDisabled}
                    className={parentStyles.textbox}
                    confirmTitle='Confirm database volume UID[:GID]'
                    confirmBody='Advanced setting. UID or UID:GID that owns the database data volume and is applied by the bootstrap chown (GID defaults to the UID, 0 = root). Empty = legacy 999:999, except Percona Server images, which use 1001. Independent of the user the process runs as. This only marks database services for reprovisioning; it does not change existing data-volume ownership. Set volume UID[:GID] to: '
                    onSave={(value) =>
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-db-volume-uid',
                          value: value
                        })
                      )
                    }
                  />
                )
              }
            ]
          : []),
        ...(selectedCluster?.config?.provDbDiskType === 'volume'
          ? [
              {
                key: 'Volume Data',
                value: (
                  <TextForm
                    value={selectedCluster?.config?.provDbVolumeData}
                    confirmTitle={`Confirm db volume data to `}
                    className={parentStyles.textbox}
                    onSave={(value) =>
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-db-volume-data',
                          value: value
                        })
                      )
                    }
                  />
                )
              }
            ]
          : []),
        ...(selectedCluster?.config?.provDbDiskType !== 'volume'
          ? [
              {
                key: 'Database Disk FS',
                value: (
                  <Dropdown
                    className={parentStyles.dropdown}
                    options={serviceFS}
                    selectedValue={selectedCluster?.config?.provDbDiskFs}
                    confirmTitle={`Confirm change DB disk FS to `}
                    onChange={(value) => {
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-db-disk-fs',
                          value: value
                        })
                      )
                    }}
                  />
                )
              }
            ]
          : []),
        ...(selectedCluster?.config?.provDbDiskType !== 'volume'
          ? [
              {
                key: 'Database Disk Pool',
                value: (
                  <Dropdown
                    className={parentStyles.dropdown}
                    options={servicePool}
                    selectedValue={selectedCluster?.config?.provDbDiskPool}
                    confirmTitle={`Confirm change DB disk pool to `}
                    onChange={(value) => {
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-db-disk-pool',
                          value: value
                        })
                      )
                    }}
                  />
                )
              }
            ]
          : []),
        ...(selectedCluster?.config?.provDbDiskType !== 'volume'
          ? [
              {
                key: 'Name',
                value: (
                  <TextForm
                    value={selectedCluster?.config?.provDbDiskDevice}
                    confirmTitle={`Confirm change DB disk device name to `}
                    className={parentStyles.textbox}
                    onSave={(value) =>
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-db-disk-device',
                          value: value
                        })
                      )
                    }
                  />
                )
              }
            ]
          : [])
      ]
    },
    {
      key: 'Proxy',
      value: [
        {
          key: 'Proxy Disk Type',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={serviceDisks}
              selectedValue={selectedCluster?.config?.provProxyDiskType}
              confirmTitle={`Confirm change proxy disk type to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-proxy-disk-type',
                    value: value
                  })
                )
              }}
            />
          )
        },
        ...(selectedCluster?.config?.provProxyDiskType === 'volume'
          ? [
              {
                key: 'Volume Data',
                value: (
                  <TextForm
                    value={selectedCluster?.config?.provProxyVolumeData}
                    confirmTitle={`Confirm db volume data to `}
                    className={parentStyles.textbox}
                    onSave={(value) =>
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-proxy-volume-data',
                          value: value
                        })
                      )
                    }
                  />
                )
              }
            ]
          : []),
        ...(selectedCluster?.config?.provProxyDiskType !== 'volume'
          ? [
              {
                key: 'Proxy Disk FS',
                value: (
                  <Dropdown
                    className={parentStyles.dropdown}
                    options={serviceFS}
                    selectedValue={selectedCluster?.config?.provProxyDiskFs}
                    confirmTitle={`Confirm change proxy disk FS to `}
                    onChange={(value) => {
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-proxy-disk-fs',
                          value: value
                        })
                      )
                    }}
                  />
                )
              }
            ]
          : []),
        ...(selectedCluster?.config?.provProxyDiskType !== 'volume'
          ? [
              {
                key: 'Proxy Disk Pool',
                value: (
                  <Dropdown
                    className={parentStyles.dropdown}
                    options={servicePool}
                    selectedValue={selectedCluster?.config?.provProxyDiskPool}
                    confirmTitle={`Confirm change proxy disk pool to `}
                    onChange={(value) => {
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-proxy-disk-pool',
                          value: value
                        })
                      )
                    }}
                  />
                )
              }
            ]
          : []),
        ...(selectedCluster?.config?.provProxyDiskType !== 'volume'
          ? [
              {
                key: 'Name',
                value: (
                  <TextForm
                    value={selectedCluster?.config?.provProxyDiskDevice}
                    confirmTitle={`Confirm change proxy disk device name to `}
                    className={parentStyles.textbox}
                    onSave={(value) => {
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'prov-proxy-disk-device',
                          value: value
                        })
                      )
                    }}
                  />
                )
              }
            ]
          : [])
      ]
    }
  ]
  return (
    <VStack>
      <TableType2 dataArray={dataObject} className={parentStyles.tableWithHelp} helpColumn={true} />
      <CommonModal
        isOpen={isHelpModalOpen}
        closeModal={() => setIsHelpModalOpen(false)}
        title={helpAction.title}
        body={helpAction.body}
        size='xl'
      />
    </VStack>
  )
}

export default OrchestratorDisks
