import React, { useEffect, useState } from 'react'
import styles from '../styles.module.scss'
import TableType2 from '../../../components/TableType2'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import Gauge from '../../../components/Gauge'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AddRemovePill from '../../../components/AddRemovePill'
import { addProxyTag, dropProxyTag } from '../../../redux/configSlice'
import { useDispatch } from 'react-redux'

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


  const dataObject = [
    ...(user?.grants['proxy-config-flag']
      ? [
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
          }
        ]
      : []),
    {
      key: 'Resources',
      value: (
        <Flex className={styles.resources}>
          <Gauge
            minValue={1}
            maxValue={25600}
            value={selectedCluster?.config?.provProxyMemory}
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
            minValue={1}
            maxValue={10000}
            value={selectedCluster?.config?.provProxyDiskSize}
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
            minValue={1}
            maxValue={256}
            value={selectedCluster?.config?.provProxyCpuCores}
            text={'Cores'}
            width={220}
            height={150}
            hideMinMax={false}
            isGaugeSizeCustomized={false}
            showStep={true}
            step={1}
            textOverlayClassName={styles.textOverlay}
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
      <TableType2
        dataArray={dataObject}
        className={styles.table}
        labelClassName={styles.label}
        valueClassName={styles.value}
        rowDivider={true}
        rowClassName={styles.row}
      />

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
