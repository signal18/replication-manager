import { Box, Flex, HStack, VStack } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import RMSwitch from '../../../components/RMSwitch'
import TableType2 from '../../../components/TableType2'
import styles from '../styles.module.scss'
import { useDispatch } from 'react-redux'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'
import AccordionComponent from '../../../components/AccordionComponent'
import AddRemovePill from '../../../components/AddRemovePill'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import { addDBTag, dropDBTag } from '../../../redux/configSlice'
import Gauge from '../../../components/Gauge'

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

  const dispatch = useDispatch()

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

  const dataObject = [
    {
      key: 'Force Write Config Files',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provDBForceWriteConfig}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-db-force-write-config?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-force-write-config' }))
          }
        />
      )
    },
    {
      key: 'Apply Dynamic Config',
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
      key: 'Connections',
      value: (
        <Flex className={styles.connections}>
          <Gauge
            minValue={200}
            maxValue={10000}
            value={selectedCluster?.config?.provDbMaxConnections}
            text={'Connections'}
            width={220}
            height={150}
            hideMinMax={false}
            isGaugeSizeCustomized={false}
            showStep={true}
            step={200}
            textOverlayClassName={styles.textOverlay}
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
            minValue={0}
            maxValue={90}
            value={selectedCluster?.config?.provDbExpireLogDays}
            text={'Expire Binglog days'}
            width={220}
            height={150}
            hideMinMax={false}
            isGaugeSizeCustomized={false}
            showStep={true}
            step={1}
            textOverlayClassName={styles.textOverlay}
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
            minValue={1}
            maxValue={25600}
            value={selectedCluster?.config?.provDbMemory}
            text={'Memory'}
            width={220}
            height={150}
            hideMinMax={false}
            isGaugeSizeCustomized={false}
            showStep={true}
            step={256}
            appendTextToValue='MB'
            textOverlayClassName={styles.textOverlay}
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
            minValue={1}
            maxValue={10000}
            value={selectedCluster?.config?.provDbDiskSize}
            text={'Disk size'}
            width={220}
            height={150}
            hideMinMax={false}
            isGaugeSizeCustomized={false}
            showStep={true}
            step={10}
            appendTextToValue='GB'
            textOverlayClassName={styles.textOverlay}
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
            minValue={1}
            maxValue={100000}
            value={selectedCluster?.config?.provDbDiskIops}
            text={'Disk IO/s'}
            width={220}
            height={150}
            hideMinMax={false}
            isGaugeSizeCustomized={false}
            showStep={true}
            step={100}
            textOverlayClassName={styles.textOverlay}
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
            minValue={1}
            maxValue={256}
            value={selectedCluster?.config?.provDbCpuCores}
            text={'Cores'}
            width={220}
            height={150}
            hideMinMax={false}
            isGaugeSizeCustomized={false}
            showStep={true}
            step={1}
            textOverlayClassName={styles.textOverlay}
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
    }
  ]

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmTitle('')
    setConfirmHandler(null)
  }

  return (
    <VStack>
      <TableType2
        dataArray={dataObject}
        className={styles.table}
        labelClassName={styles.label}
        valueClassName={styles.value}
        rowDivider={true}
        rowClassName={styles.row}
      />
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
                        const isAdded = usingDBServerTags.find((x) => x.name === tag.name)
                        if (isAdded) {
                          return null
                        }
                        return (
                          <AddRemovePill
                            text={tag.name}
                            onAdd={(title) => {
                              setConfirmTitle(title)
                              setIsConfirmModalOpen(true)
                              setConfirmHandler(
                                () => () => dispatch(addDBTag({ clusterName: selectedCluster?.name, tag: tag.name }))
                              )
                            }}
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
              {usingDBServerTags.map((tag) => (
                <AddRemovePill
                  text={tag?.name}
                  onRemove={(title) => {
                    setConfirmTitle(title)
                    setIsConfirmModalOpen(true)
                    setConfirmHandler(
                      () => () => dispatch(dropDBTag({ clusterName: selectedCluster?.name, tag: tag.name }))
                    )
                  }}
                  used={true}
                  category={tag?.category}
                />
              ))}
            </HStack>
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
    </VStack>
  )
}

export default DBConfigs
