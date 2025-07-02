import React, { useEffect, useMemo, useState } from 'react'
import styles from '../styles.module.scss'
import { Flex, HStack, Text, VStack } from '@chakra-ui/react'
import { useDispatch } from 'react-redux'
import Gauge from '../../../../../components/Gauge'
import { convertSize } from '../../../../../utility/common'
import { setAppSetting } from '../../../../../redux/settingsSlice'
import TableType2 from '../../../../../components/TableType2'
import ConfirmModal from '../../../../../components/Modals/ConfirmModal'

function AppCredit({ clusterName, appId, config, appConfig, user }) {
    const dispatch = useDispatch()
    const [confirmState, setConfirmState] = useState({
        isOpen: false,
        title: '',
        body: '',
        handler: null
    })
    const { isOpen, title, body, handler } = confirmState

    const numAgents = useMemo(() => {
        return appConfig?.provAppAgents?.split(",").filter(agent => agent.trim() !== '').length || 0
    }, [appConfig?.provAppAgents])

    const closeConfirmModal = () => {
        setConfirmState({
            isOpen: false,
            title: '',
            body: '',
            handler: null
        })
    }

    const dataObject = [
        {
            key: "Cloud18 Credits Available",
            value: config?.cloud18ApplicationCredits ? (<Text>{config.cloud18ApplicationCredits - config.cloud18ApplicationCreditsUsed} / {config.cloud18ApplicationCredits}</Text>) : (<Text>{'Not set'}</Text>),
        },
        {
            key: 'Resources',
            value: (
                <Flex direction={"column"}>
                    <Flex className={styles.resources}>
                        <Gauge
                            isDisabled={user?.grants['app-config-flag'] == false}
                            minValue={256}
                            maxValue={256 * 1024}
                            value={convertSize(appConfig?.provAppMemory, "M", "M")}
                            text={'Memory'}
                            width={220}
                            height={150}
                            hideMinMax={false}
                            isGaugeSizeCustomized={false}
                            appendTextToValue='MB'
                            textOverlayClassName={styles.textOverlay}
                        />
                        <Gauge
                            isDisabled={user?.grants['app-config-flag'] == false}
                            minValue={1}
                            maxValue={10000}
                            value={convertSize(appConfig?.provAppDiskSize, "G", "G")}
                            text={'Disk size'}
                            width={220}
                            height={150}
                            hideMinMax={false}
                            isGaugeSizeCustomized={false}
                            appendTextToValue='GB'
                            textOverlayClassName={styles.textOverlay}
                        />
                        <Gauge
                            isDisabled={user?.grants['app-config-flag'] == false}
                            minValue={1}
                            maxValue={256}
                            value={appConfig?.provAppCpuCores}
                            text={'Cores'}
                            width={220}
                            height={150}
                            hideMinMax={false}
                            isGaugeSizeCustomized={false}
                            textOverlayClassName={styles.textOverlay}
                        />
                    </Flex>
                    <Flex direction={"row"} justifyContent={"center"} alignItems={"center"}>
                        <Gauge
                            isDisabled={user?.grants['app-config-flag'] == false}
                            minValue={1}
                            maxValue={10000}
                            value={appConfig?.provAppCreditPlanned}
                            appendTextToValue={' Credits'}
                            text={'Credits'}
                            width={220}
                            height={150}
                            hideMinMax={false}
                            isGaugeSizeCustomized={false}
                            showStep={true}
                            step={numAgents}
                            textOverlayClassName={styles.textOverlay}
                            handleStepChange={(value) => {
                                setConfirmState({
                                    isOpen: true,
                                    title: `Confirm change for 'prov-app-credit-planned' to ${value}`,
                                    body: `Are you sure you want to change the 'prov-app-credit-planned' to ${value}?`,
                                    handler: () => {
                                        dispatch(
                                            setAppSetting({
                                                clusterName: clusterName,
                                                appId: appId,
                                                setting: 'prov-app-credit-planned',
                                                value: value
                                            })
                                        )
                                    }
                                })
                            }}
                        />
                    </Flex>
                </Flex>
            )
        }
    ]
    return (
        <VStack>
            <TableType2 dataArray={dataObject} className={styles.table} />
            <ConfirmModal
                isOpen={isOpen}
                closeModal={closeConfirmModal}
                title={title}
                body={body}
                onConfirmClick={() => {
                    console.log('onConfirmClick clicked', handler)
                    handler()
                    closeConfirmModal()
                }}
            />
        </VStack>
    )
}

export default AppCredit
