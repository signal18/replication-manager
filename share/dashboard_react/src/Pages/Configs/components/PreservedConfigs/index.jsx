import { Box, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef } from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseVariables, preserveVariable } from '../../../../redux/clusterSlice'
import { isEqual } from 'lodash'
import RMIconButton from '../../../../components/RMIconButton'
import { TbTrash } from 'react-icons/tb'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import ComboBox from '../../../../components/ComboBox'
import { DataTable } from '../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import TextForm from '../../../../components/TextForm'
import RMSwitch from '../../../../components/RMSwitch'
import { switchSetting } from '../../../../redux/settingsSlice'
import TableType2 from '../../../../components/TableType2'

function PreservedConfigs({ selectedCluster }) {
    const dispatch = useDispatch()
    const columnHelper = createColumnHelper()

    const {
        cluster: {
            database: { variables },
            clusterMaster,
        }
    } = useSelector((state) => state)

    const [action, setAction] = useState({ type: '', title: '', payload: '' })
    const [preservedConfigs, setPreservedConfigs] = useState([])
    const [variablesData, setVariablesData] = useState([])
    const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(null)
    const prevVariablesRef = useRef(variables)
    const { type, title, payload } = action

    useEffect(() => {
        if (selectedCluster) {
          const dbId = clusterMaster?.id
            || selectedCluster?.dbServers?.[0]
            || '';
          dispatch(getDatabaseVariables({
            clusterName: selectedCluster?.name,
            serviceName: 'variables',
            dbId,
            diff: false,
          }))
        }
      }, [selectedCluster?.name, selectedCluster?.dbServers?.[0], clusterMaster?.id])

    useEffect(() => {
        if (!isEqual(variables, prevVariablesRef.current) || (variables?.length > 0 && variablesData.length === 0)) {
            setVariablesData(variables?.map(x => x.variableName) || [])
            prevVariablesRef.current = variables
        }
    }, [variables])

    useEffect(() => {
        setPreservedConfigs(selectedCluster?.config?.provDbConfigPreserveVars?.toLowerCase().split(';').filter(Boolean))
    }, [selectedCluster?.config?.provDbConfigPreserveVars])

    const openConfirmModal = (e) => {
        setIsConfirmModalOpen(true)
    }

    const closeConfirmModal = () => {
        setIsConfirmModalOpen(false)
    }

    const handleComboBox = (value) => {
        setAction({ type: "preserve-true", title: "Are you sure to preserve variable? This will prevent configurator to change the value for whole cluster", payload: value });
        openConfirmModal();
    }

    const handleRemoveEntry = (value) => {
        setAction({ type: "preserve-false", title: "Are you sure to remove variable's preservation? This will allow configurator to change the value for whole cluster", payload: value });
        openConfirmModal();
    }

    const handleConfirm = () => {
        if (type === 'preserve-true') {
            dispatch(preserveVariable({ clusterName: selectedCluster?.name, preserve: true, variableName: payload }))
            setPreservedConfigs((prev) => {
                if (!prev.includes(payload)) {
                    return [...prev, payload]
                }
                return prev
            })
        } else if (type === 'preserve-false') {
            dispatch(preserveVariable({ clusterName: selectedCluster?.name, preserve: false, variableName: payload }))
            setPreservedConfigs((prev) => {
                const index = prev.indexOf(payload)
                if (index > -1) {
                    prev.splice(index, 1)
                }
                return [...prev]
            })
        }
        closeConfirmModal()
    }

    const tableData = useMemo(() => {
        return preservedConfigs.map((item = '') => {
            const [vname, vval] = item.split('=').filter(Boolean)
            return {
                variableName: vname,
                value: vval || '',
                rowColor: variablesData.find((x) => x === vname) ? '' : 'orange'
            }
        })
    }, [preservedConfigs,variablesData])

    const columns = useMemo(
        () => [
            columnHelper.accessor("variableName", {
                header: 'Variable',
                size: 100,
                maxSize: 200
            }),
            columnHelper.accessor("value", {
                header: 'Value',
                size: 100,
                maxSize: 200,
                cell: (info) => (<TextForm value={info.getValue()} confirmTitle={`Confirm set value to `} maxLength={1024} className={styles.textContainer} onSave={(value) => {dispatch(preserveVariable({ clusterName: selectedCluster?.name, preserve: true, variableName: info.row.original.variableName + "=" + value }))}}/>)
            }),
            columnHelper.display({
                id: 'actions',
                header: 'Actions',
                cell: (info) => (
                    <RMIconButton
                        tooltip="Preserve: False"
                        icon={TbTrash}
                        onClick={() => handleRemoveEntry(info.row.original.variableName)}
                    />
                )
            })
        ],
        [preservedConfigs]
    )
    
    const dataObject = [
        {
          key: 'Enable Preserve Variables',
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
        }
    ]

    return (
        <VStack className={styles.contentContainer}>
            <TableType2 dataArray={dataObject} className={styles.table} />
            <Box w={'100%'}>
                <ComboBox
                    className={styles.comboBox}
                    placeholder="New Preserved Variable"
                    options={variablesData}
                    onConfirm={handleComboBox}
                    clearAfterConfirm={true}
                />
            </Box>
            <Box className={`${styles.tableContainer} ${styles.variableContainer}`} overflow={'auto'}>
                <DataTable data={tableData} columns={columns} className={styles.table} enablePagination={true} />
            </Box>
            {isConfirmModalOpen && <ConfirmModal title={title} isOpen={isConfirmModalOpen} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
        </VStack>
    )
}

export default PreservedConfigs
