import React, { useEffect, useMemo, useState } from 'react'
import styles from '../styles.module.scss'
import { Flex, HStack, Text, VStack } from '@chakra-ui/react'
import { useDispatch } from 'react-redux'
import Gauge from '../../../../../components/Gauge'
import { convertSize } from '../../../../../utility/common'
import { setAppSetting } from '../../../../../redux/settingsSlice'
import TableType2 from '../../../../../components/TableType2'
import ConfirmModal from '../../../../../components/Modals/ConfirmModal'
import NumberInput from '../../../../../components/NumberInput'
import { showErrorToast } from '../../../../../redux/toastSlice'

function AppCredit({ clusterName, appId, config, appConfig, user }) {
    const dispatch = useDispatch()
    const [confirmState, setConfirmState] = useState({
        isOpen: false,
        title: '',
        body: '',
        handler: null
    })
    const { isOpen, title, body, handler } = confirmState

    const creditStep = useMemo(() => {
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

    const allowEdit = user?.grants['app-config-flag'] !== false
    const clusterCredits = config?.cloud18ApplicationCredits || 0
    const clusterCreditsUsed = config?.cloud18ApplicationCreditsUsed || 0
    const clusterCreditsAvailable = clusterCredits - clusterCreditsUsed
    const provAppMemory = convertSize(appConfig?.provAppMemory, "M", "M")
    const provAppDiskSize = convertSize(appConfig?.provAppDiskSize, "G", "G")
    const provAppCpuCores = appConfig?.provAppCpuCores || 0
    const appCredits = appConfig?.provAppCreditPlanned || 0

    const dataObject = useMemo(() => [
        {
            key: "Cloud18 Credits Available",
            value: clusterCredits ? (<Text>{clusterCreditsAvailable} / {clusterCredits}</Text>) : (<Text>{'Not set'}</Text>),
        },
        {
            key: 'Resources',
            value: (
                <Flex className={styles.resources}>
                    <Gauge
                        isDisabled={true}
                        minValue={256}
                        maxValue={256 * 1024}
                        value={provAppMemory}
                        text={'Memory'}
                        width={220}
                        height={150}
                        hideMinMax={false}
                        isGaugeSizeCustomized={false}
                        appendTextToValue='MB'
                        textOverlayClassName={styles.textOverlay}
                    />
                    <Gauge
                        isDisabled={true}
                        minValue={1}
                        maxValue={10000}
                        value={provAppDiskSize}
                        text={'Disk size'}
                        width={220}
                        height={150}
                        hideMinMax={false}
                        isGaugeSizeCustomized={false}
                        appendTextToValue='GB'
                        textOverlayClassName={styles.textOverlay}
                    />
                    <Gauge
                        isDisabled={true}
                        minValue={1}
                        maxValue={256}
                        value={provAppCpuCores}
                        text={'Cores'}
                        width={220}
                        height={150}
                        hideMinMax={false}
                        isGaugeSizeCustomized={false}
                        textOverlayClassName={styles.textOverlay}
                    />
                </Flex>
            )
        },
        {
            key: 'Credits',
            value: (
                <Flex className={styles.resources}>
                    { creditStep ? (<NumberInput
                        isDisabled={!allowEdit}
                        minValue={creditStep}
                        maxValue={10000}
                        step={creditStep}
                        value={appCredits}
                        showConfirmModal={true}
                        confirmTitle={`Confirm change for 'prov-app-credit-planned'`}
                        confirmBody={`Are you sure you want to change the 'prov-app-credit-planned' value?`}
                        showEditButton={true}
                        onConfirmValidator={(value) => {
                            if (value % creditStep !== 0) {
                                dispatch(showErrorToast(`Value must be a multiple of ${creditStep}`))
                                return false
                            }
                            return true
                        }}
                        onConfirm={(value) => {
                            dispatch(
                                setAppSetting({
                                    clusterName: clusterName,
                                    appId: appId,
                                    setting: 'prov-app-credit-planned',
                                    value: value
                                })
                            )
                        }}
                    />) : (
                        <Text>No Agents Available</Text>
                    )}
                </Flex>
            )
        }
    ], [clusterCreditsAvailable, clusterCredits, provAppMemory, provAppDiskSize, provAppCpuCores, appCredits, creditStep, allowEdit, clusterName, appId, dispatch])
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
