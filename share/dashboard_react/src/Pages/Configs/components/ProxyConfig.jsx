import React, { useEffect, useState } from 'react'
import styles from '../styles.module.scss'
import TableType2 from '../../../components/TableType2'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import Gauge from '../../../components/Gauge'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AddRemovePill from '../../../components/AddRemovePill'
import { addProxyTag, dropProxyTag } from '../../../redux/configSlice'
import { useDispatch } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import { convertSize } from '../../../utility/common'

function ProxyConfig({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [availableTags, setAvailableTags] = useState([])
  const [usingTags, setUsingTags] = useState([])
  useEffect(() => {
    if (selectedCluster?.configurator?.configPrxTags?.length > 0) {
      setAvailableTags(selectedCluster.configurator.configPrxTags)
    }
  }, [selectedCluster?.configurator?.configPrxTags])

  useEffect(() => {
    if (selectedCluster?.configurator?.proxyServersTags?.length > 0) {
      setUsingTags(selectedCluster.configurator.proxyServersTags)
    }
  }, [selectedCluster?.configurator?.proxyServersTags])

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmTitle('')
    setConfirmHandler(null)
  }

  // {
  //   key: 'Cluster Proxy Start Fetch Config',
  //   value: (
  //     <RMSwitch
  //       isChecked={selectedCluster?.config?.provProxyStartFetchConfig}
  //       isDisabled={user?.grants['cluster-settings'] == false}
  //       confirmTitle={'Confirm switch settings for prov-proxy-start-fetch-config?'}
  //       onChange={() =>
  //         dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-proxy-start-fetch-config' }))
  //       }
  //     />
  //   )
  // },
  // {
  //   key: 'Generate All Proxy Config Files',
  //   value: (
  //     <RMIconButton icon={HiRefresh} onClick={() => { 
  //       setConfirmTitle('Confirm generate Proxy Config from configurator settings to monitor datadir?'); 
  //       setIsConfirmModalOpen(true)
  //       setConfirmHandler(() => () => dispatch(generateAllConfig({ clusterName: selectedCluster?.name, type: 'proxy'})))
  //     }} />
  //   )
  // },

  const dataObject = [
    {
      key: 'Manage Tags',
      value: (
        <VStack className={styles.configTagContainer}>
          <VStack className={`${styles.availableTags} ${styles.proxyTags}`}>
            <h4 className={styles.sectionTitle}>{'Available Tags'}</h4>
            <HStack className={styles.tags}>
              {availableTags.map((tag) => {
                const isAdded = usingTags.find((x) => x.name === tag.name)
                if (isAdded) {
                  return null
                }
                return (
                  <AddRemovePill
                    isDisabled={user?.grants['proxy-config-flag'] == false}
                    text={tag.name}
                    onAdd={(title) => {
                      setConfirmTitle(title)
                      setIsConfirmModalOpen(true)
                      setConfirmHandler(
                        () => () => dispatch(addProxyTag({ clusterName: selectedCluster?.name, tag: tag.name }))
                      )
                    }}
                  />
                )
              })}
            </HStack>
          </VStack>
          <VStack className={`${styles.addedTags} ${styles.proxyTags}`}>
            <h4 className={styles.sectionTitle}>{'Using Tags'}</h4>
            <HStack className={`${styles.tags} `}>
              {usingTags.map((tag) => {
                return (
                  <AddRemovePill
                    isDisabled={user?.grants['proxy-config-flag'] == false}
                    text={tag}
                    onRemove={(title) => {
                      setConfirmTitle(title)
                      setIsConfirmModalOpen(true)
                      setConfirmHandler(
                        () => () => dispatch(dropProxyTag({ clusterName: selectedCluster?.name, tag: tag }))
                      )
                    }}
                    used={true}
                  />
                )
              })}
            </HStack>
          </VStack>
        </VStack>
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
            value={convertSize(selectedCluster?.config?.provProxyMemory, "M", "M")}
            text={'Memory'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={256}
            appendTextToValue='MB'
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm proxy memory change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-proxy-memory',
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
            value={convertSize(selectedCluster?.config?.provProxyDiskSize, "G", "G")}
            text={'Disk size'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={10}
            appendTextToValue='GB'
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm proxy disk size change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-proxy-disk-size',
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
            value={selectedCluster?.config?.provProxyCpuCores}
            text={'Cores'}
            width={150}
            height={105}
            hideMinMax={false}
            showStep={true}
            step={1}
            handleStepChange={(value) => {
              setConfirmTitle(`Confirm proxy cpu cores change to ${value}`)
              setIsConfirmModalOpen(true)
              setConfirmHandler(
                () => () =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-proxy-cpu-cores',
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
  return (
    <VStack>
      <TableType2 dataArray={dataObject} className={styles.table} />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            console.log('onConfirmClick clicked', confirmHandler)
            confirmHandler()
            closeConfirmModal()
          }}
        />
      )}
    </VStack>
  )
}

export default ProxyConfig
