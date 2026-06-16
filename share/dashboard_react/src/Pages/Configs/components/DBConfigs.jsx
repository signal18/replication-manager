import { Alert, AlertIcon, Box, Button, Flex, HStack, Text, VStack } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import RMSwitch from '../../../components/RMSwitch'
import TableType2 from '../../../components/TableType2'
import styles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'
import AccordionComponent from '../../../components/AccordionComponent'
import AddRemovePill from '../../../components/AddRemovePill'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import TagContentModal from '../../../components/Modals/TagContentModal'
import ComplianceDiffModal from '../../../components/Modals/ComplianceDiffModal'
import CommonModal from '../../../components/Modals/CommonModal'
import { addDBTag, dropDBTag, generateAllConfig } from '../../../redux/configSlice'
import { configService } from '../../../services/configService'
import Gauge from '../../../components/Gauge'
import RMIconButton from '../../../components/RMIconButton'
import { HiRefresh } from 'react-icons/hi'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import modalStyles from '../../../components/Modals/styles.module.scss'

import PreservedVariablesEditor from '../../../components/PreservedVariablesEditor'
import ConfigFilesPanel from '../../../components/ConfigFilesPanel'
import MemoryPctEditor from '../../../components/MemoryPctEditor'
import { convertSize } from '../../../utility/common'

function DBConfigs({ selectedCluster, user }) {
  const [replicationTags, setReplicationTags] = useState([])
  const [logTags, setLogsTags] = useState([])
  const [engineTags, setEngineTags] = useState([])
  const [optimizerTags, setOptimizerTags] = useState([])
  const [diskTags, setDiskTags] = useState([])
  const [networkTags, setNetworkTags] = useState([])
  const [securityTags, setSecurityTags] = useState([])
  const [charsetTags, setCharsetTags] = useState([])
  const [systemTags, setSystemTags] = useState([])
  const [usingDBServerTags, setUsingDBServerTags] = useState([])

  const [configTagData, setConfigTagData] = useState([])

  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)

  // Tag content modal state (includes doc help section from the same API response)
  const [tagContentModal, setTagContentModal] = useState({ open: false, tagName: '', data: null })

  // Compliance diff modal state
  const [complianceDiffModal, setComplianceDiffModal] = useState({ open: false, data: null, loading: false })
  const [acceptLoading, setAcceptLoading] = useState(false)

  // Check if WARN0168 (pending compliance update) is active
  const clusterAlerts = useSelector((state) => state?.cluster?.clusterAlerts)
  const hasPendingCompliance = clusterAlerts?.warnings?.some((w) => w.number === 'WARN0168')

  const handleReviewCompliance = async () => {
    setComplianceDiffModal({ open: true, data: null, loading: true })
    try {
      const { data } = await configService.getComplianceDiff(selectedCluster?.name, baseURL)
      setComplianceDiffModal({ open: true, data, loading: false })
    } catch {
      setComplianceDiffModal({ open: true, data: null, loading: false })
    }
  }

  const handleAcceptCompliance = async () => {
    setAcceptLoading(true)
    try {
      await configService.acceptCompliance(selectedCluster?.name, baseURL)
      setComplianceDiffModal({ open: false, data: null, loading: false })
    } catch (err) {
      // keep modal open on error
    }
    setAcceptLoading(false)
  }

  // Help modal state
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

  const baseURL = useSelector((state) => state?.auth?.baseURL || '')

  const dispatch = useDispatch()

  const handleViewContent = async (tagName) => {
    try {
      const { data } = await configService.getTagContent(selectedCluster?.name, tagName, baseURL)
      setTagContentModal({ open: true, tagName, data })
    } catch {
      setTagContentModal({ open: true, tagName, data: null })
    }
  }

  useEffect(() => {
    if (selectedCluster?.configurator?.configTags?.length > 0) {
      const allTags = selectedCluster.configurator.configTags
      setReplicationTags(allTags.filter((tag) => tag.category === 'replication'))
      setLogsTags(allTags.filter((tag) => tag.category === 'log'))
      setEngineTags(allTags.filter((tag) => tag.category === 'engine'))
      setOptimizerTags(allTags.filter((tag) => tag.category === 'optimizer'))
      setDiskTags(allTags.filter((tag) => tag.category === 'disk'))
      setNetworkTags(allTags.filter((tag) => tag.category === 'network'))
      setSecurityTags(allTags.filter((tag) => tag.category === 'security'))
      setCharsetTags(allTags.filter((tag) => tag.category === 'charset'))
      setSystemTags(allTags.filter((tag) => tag.category === 'system'))
    }
  }, [selectedCluster?.configurator?.configTags])

  useEffect(() => {
    if (selectedCluster?.configurator?.dbServersTags?.length > 0) {
      const dbServersTagsWithName = selectedCluster.configurator.dbServersTags.map((tag) => {
        const repTag = replicationTags.length > 0 ? replicationTags.find((x) => x.name === tag) : null
        if (repTag) {
          return repTag
        }
        const logTag = logTags.length > 0 ? logTags.find((x) => x.name === tag) : null
        if (logTag) {
          return logTag
        }

        const engineTag = engineTags.length > 0 ? engineTags.find((x) => x.name === tag) : null
        if (engineTag) {
          return engineTag
        }

        const optTag = optimizerTags.length > 0 ? optimizerTags.find((x) => x.name === tag) : null
        if (optTag) {
          return optTag
        }

        const diskTag = diskTags.length > 0 ? diskTags.find((x) => x.name === tag) : null
        if (diskTag) {
          return diskTag
        }

        const networkTag = networkTags.length > 0 ? networkTags.find((x) => x.name === tag) : null
        if (networkTag) {
          return networkTag
        }

        const secTag = securityTags.length > 0 ? securityTags.find((x) => x.name === tag) : null
        if (secTag) {
          return secTag
        }

        const charTag = charsetTags.length > 0 ? charsetTags.find((x) => x.name === tag) : null
        if (charTag) {
          return charTag
        }

        const systemTag = systemTags.length > 0 ? systemTags.find((x) => x.name === tag) : null
        if (systemTag) {
          return systemTag
        }
      })

      setUsingDBServerTags(dbServersTagsWithName)

      setConfigTagData([
        { key: 'Replication', value: replicationTags },
        { key: 'Logs', value: logTags },
        { key: 'Engines', value: engineTags },
        { key: 'Optimizer', value: optimizerTags },
        { key: 'Disk', value: diskTags },
        { key: 'Network', value: networkTags },
        { key: 'Security', value: securityTags },
        { key: 'Charsets', value: charsetTags },
        { key: 'System', value: systemTags }
      ])
    }
  }, [
    replicationTags,
    logTags,
    engineTags,
    optimizerTags,
    diskTags,
    networkTags,
    securityTags,
    charsetTags,
    systemTags,
    selectedCluster?.configurator?.dbServersTags
  ])

  const hFetchConfig = `**Fetch Config on Start**\n\nWhen enabled, database servers fetch their configuration from replication-manager on startup.\n\nThe generated config is pushed to each server's data directory and applied when the database process starts.\n\nConfig: \`prov-db-start-fetch-config\``
  const hDynamicConfig = `**Apply Dynamic Config**\n\nWhen enabled, replication-manager applies configuration changes dynamically using \`SET GLOBAL\` statements without requiring a database restart.\n\nOnly variables that support dynamic changes are applied this way. Variables requiring a restart are written to the config file for the next restart.\n\nConfig: \`prov-db-apply-dynamic-config\``
  const hRefreshConfig = `**Refresh Variables and DB Config**\n\nRegenerates the configuration files for all database servers in this cluster from the compliance module and the current tag selection.\n\nThis rebuilds the config tarball at \`{datadir}/config.tar.gz\` with all tag-specific cnf files merged with preserved variables and defaults.`
  const hAutoUpdateCompliance = `**Auto-Update Compliance**\n\nThe compliance module contains replication-manager's best practices for database and proxy configuration. It defines which variables are set for each configuration tag.\n\nWhen enabled (default), compliance updates from the back office or new replication-manager releases are **applied automatically**. Your preserved variables are never overwritten — they always take priority over compliance defaults.\n\nWhen disabled, a warning (WARN0168) is raised when new compliance is available. You can review the changes (added, removed, or modified tags) and accept when ready. This is recommended for production environments where you want to review best practice changes before they take effect.\n\nConfig: \`prov-auto-update-compliance\``

  const dataObject = [
    {
      key: 'Cluster DB Start Fetch Config',
      help: h(hFetchConfig, 'Fetch Config on Start'),
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provDbStartFetchConfig}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-db-start-fetch-config?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-start-fetch-config' }))
          }
        />
      )
    },
    {
      key: 'Apply Dynamic Config',
      help: h(hDynamicConfig, 'Apply Dynamic Config'),
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provDBApplyDynamicConfig}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-db-apply-dynamic-config?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-apply-dynamic-config' }))
          }
        />
      )
    },
    {
      key: 'Auto-Update Compliance',
      help: h(hAutoUpdateCompliance, 'Auto-Update Compliance'),
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provAutoUpdateCompliance}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-auto-update-compliance?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-auto-update-compliance' }))
          }
        />
      )
    },
    {
      key: 'Refresh Variables and DB Config',
      help: h(hRefreshConfig, 'Refresh Variables and DB Config'),
      value: (
        <RMIconButton isDisabled={user?.grants['proxy-config-flag'] == false} icon={HiRefresh} onClick={() => {
          setConfirmTitle('Confirm refresh variables and db config?')
          setIsConfirmModalOpen(true)
          setConfirmHandler(() => () => dispatch(generateAllConfig({ clusterName: selectedCluster?.name, type: 'db'})))
        }} />
      )
    },
    {
      key: 'Connections',
      value: (
        <Flex className={styles.connections}>
          <Gauge
            isDisabled={user?.grants['proxy-config-flag'] == false}
            minValue={200}
            maxValue={10000}
            value={selectedCluster?.config?.provDbMaxConnections}
            text={'Connections'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={200}
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm connections change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-max-connections',
                      value: value
                    })
                  )
              )
            }}
          />
          <Gauge
            isDisabled={user?.grants['proxy-config-flag'] == false}
            minValue={0}
            maxValue={90}
            value={selectedCluster?.config?.provDbExpireLogDays}
            text={'Expire Binlog days'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={1}
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm expire binlog days change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-expire-log-days',
                      value: value
                    })
                  )
              )
            }}
          />
        </Flex>
      )
    },
    {
      key: 'Resources',
      value: (
        <Flex className={styles.resources}>
          <Gauge
            isDisabled={user?.grants['proxy-config-flag'] == false}
            minValue={1}
            maxValue={25600}
            value={convertSize(selectedCluster?.config?.provDbMemory,"M","M")}
            text={'Memory'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={256}
            appendTextToValue='MB'
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm memory change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-memory',
                      value: value
                    })
                  )
              )
            }}
          />
          <Gauge
            isDisabled={user?.grants['proxy-config-flag'] == false}
            minValue={1}
            maxValue={10000}
            value={convertSize(selectedCluster?.config?.provDbDiskSize,"G","G")}
            text={'Disk size'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={10}
            appendTextToValue='GB'
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm disk size change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-disk-size',
                      value: value
                    })
                  )
              )
            }}
          />
          <Gauge
            isDisabled={user?.grants['proxy-config-flag'] == false}
            minValue={1}
            maxValue={100000}
            value={selectedCluster?.config?.provDbDiskIops}
            text={'Disk IO/s'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={100}
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm disk io/s change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-disk-iops',
                      value: value
                    })
                  )
              )
            }}
          />
          <Gauge
            isDisabled={user?.grants['proxy-config-flag'] == false}
            minValue={1}
            maxValue={256}
            value={selectedCluster?.config?.provDbCpuCores}
            text={'Cores'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={1}
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm cpu cores change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-cpu-cores',
                      value: value
                    })
                  )
              )
            }}
          />
        </Flex>
      )
    },
    {
      key: 'Memory Allocation',
      value: (
        <Flex direction='column' gap={4} w='100%'>
          <MemoryPctEditor
            label='Shared Buffers'
            value={selectedCluster?.config?.provDbMemorySharedPct}
            isDisabled={user?.grants['proxy-config-flag'] == false}
            onSave={(value) => {
              setConfirmTitle('Confirm shared buffer memory allocation change?')
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-memory-shared-pct',
                      value: value
                    })
                  )
              )
            }}
          />
          <MemoryPctEditor
            label='Per-Thread Buffers'
            value={selectedCluster?.config?.provDbMemoryThreadedPct}
            isDisabled={user?.grants['proxy-config-flag'] == false}
            onSave={(value) => {
              setConfirmTitle('Confirm per-thread memory allocation change?')
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-db-memory-threaded-pct',
                      value: value
                    })
                  )
              )
            }}
          />
        </Flex>
      )
    }
  ]

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmTitle('')
    setConfirmHandler(null)
  }

  return (
    <VStack>
      {hasPendingCompliance && (
        <Alert status='warning' borderRadius='md' w='100%'>
          <AlertIcon />
          <Text fontSize='sm' flex='1'>
            A new compliance update is available from the back office. Review the changes before accepting.
          </Text>
          <HStack spacing={2}>
            <Button size='sm' variant='outline' colorScheme='orange' onClick={handleReviewCompliance}>
              Review Changes
            </Button>
            <Button size='sm' colorScheme='blue' isLoading={acceptLoading} onClick={handleAcceptCompliance}>
              Accept
            </Button>
          </HStack>
        </Alert>
      )}
      <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      {user?.grants['db-config-flag'] && (
        <HStack className={styles.configTagContainer}>
          <VStack className={styles.availableTags}>
            <h4 className={styles.sectionTitle}>{'Available Tags'}</h4>
            {configTagData.map((tagData) => {
              return (
                <AccordionComponent
                  heading={tagData.key}
                  className={styles.accordion}
                  headerClassName={styles.accordionHeader}
                  panelClassName={styles.accordionBody}
                  body={
                    <HStack className={styles.tags}>
                      {tagData.value.map((tag) => {
                        const isAdded = usingDBServerTags?.find((x) => x?.name === tag.name)
                        if (isAdded) {
                          return null
                        }
                        return (
                          <AddRemovePill
                            isDisabled={user?.grants['proxy-config-flag'] == false}
                            key={tag.name}
                            text={tag.name}
                            onAdd={(title) => {
                              setConfirmTitle(title)
                              setIsConfirmModalOpen(true)
                              setConfirmHandler(
                                () => () => dispatch(addDBTag({ clusterName: selectedCluster?.name, tag: tag.name }))
                              )
                            }}
                            onViewContent={handleViewContent}
                          />
                        )
                      })}
                    </HStack>
                  }
                />
              )
            })}
          </VStack>
          <VStack className={styles.addedTags}>
            <h4 className={styles.sectionTitle}>{'Using Tags'}</h4>
            <HStack className={`${styles.tags} `}>
              {usingDBServerTags
                .filter((tag) => tag?.name)
                .map((tag) => (
                  <AddRemovePill
                    key={tag?.name}
                    text={tag?.name}
                    onViewContent={handleViewContent}
                    onRemove={(title) => {
                      setConfirmTitle(title)
                      setIsConfirmModalOpen(true)
                      setConfirmHandler(
                        () => () => dispatch(dropDBTag({ clusterName: selectedCluster?.name, tag: tag?.name }))
                      )
                    }}
                    used={true}
                    category={tag?.category}
                  />
                ))}
            </HStack>
            <ConfigFilesPanel selectedCluster={selectedCluster} />
            <AccordionComponent
                  heading={'Cluster Preserved Variables (Table & Editor)'}
                  className={styles.accordion}
                  headerClassName={styles.accordionHeader}
                  panelClassName={styles.accordionBody}
                  body={<PreservedVariablesEditor clusterName={selectedCluster?.name} user={user} />}
                />
          </VStack>
        </HStack>
      )}

      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            confirmHandler()
            closeConfirmModal()
          }}
        />
      )}

      <TagContentModal
        isOpen={tagContentModal.open}
        closeModal={() => setTagContentModal({ open: false, tagName: '', data: null })}
        tagName={tagContentModal.tagName}
        data={tagContentModal.data}
      />

      <CommonModal
        isOpen={isHelpModalOpen}
        closeModal={() => setIsHelpModalOpen(false)}
        title={helpAction.title}
        body={helpAction.body}
        size='xl'
      />

      <ComplianceDiffModal
        isOpen={complianceDiffModal.open}
        closeModal={() => setComplianceDiffModal({ open: false, data: null, loading: false })}
        diffData={complianceDiffModal.data}
        loading={complianceDiffModal.loading}
        onAccept={hasPendingCompliance ? handleAcceptCompliance : undefined}
        acceptLoading={acceptLoading}
      />

    </VStack>
  )
}

export default DBConfigs
