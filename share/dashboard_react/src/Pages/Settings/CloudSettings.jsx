import { Flex, HStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArrayForDropdown, formatBytes } from '../../utility/common'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import SetCredentialsModal from '../../components/Modals/SetCredentialsModal'
import RMIconButton from '../../components/RMIconButton'
import { HiKey } from 'react-icons/hi'
import { TbDatabaseStar, TbDatabaseX, TbDeviceDesktopStar, TbDeviceDesktopX, TbUserCancel } from 'react-icons/tb'
import { acceptExternalRole, endExternalRole, refuseExternalRole, subscribeExternalRole } from '../../redux/clusterSlice'
import TextInputModal from '../../components/Modals/TextInputModal'
import ConfirmModal from '../../components/Modals/ConfirmModal'

function CloudSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  const [action, setAction] = useState({ type: '', title: '', payload: '' })
  const [planOptions, setPlanOptions] = useState([])
  const [dbopsOptions, setDbopsOptions] = useState([])
  const [partnerOptions, setPartnerOptions] = useState([])
  const [credentialType, setCredentialType] = useState('')
  const [isCredentialModalOpen, setIsCredentialModalOpen] = useState(false)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(null)
  const [isTextModalOpen, setIsTextModalOpen] = useState(false)
  const { type, title, payload } = action

  const getPlanOptions = (plist = []) => [{ name: "No Plan", value: '' }, ...plist?.map((obj) => ({ name: obj.plan, value: obj.plan }))]
  const getPartnerOptions = (plist = [], role) => [{ name: "No Partner", value: '' }, ...plist?.map((obj) => ({ name: obj.Name, value: role === 'extdbops' ? obj.DbopsEmail : obj.SysopsEmail }))]

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const openTextModal = () => {
    setIsTextModalOpen(true)
  }

  const handleConfirm = (value) => {
    if (type === 'accept-extdbops') {
      dispatch(acceptExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops' }))
    } else if (type === 'reject-extdbops') {
      dispatch(refuseExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops', reason: value }))
    } else if (type === 'end-extdbops') {
      dispatch(endExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops', reason: value }))
    } else if (type === 'accept-extsysops') {
      dispatch(acceptExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops' }))
    } else if (type === 'reject-extsysops') {
      dispatch(refuseExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops', reason: value }))
    } else if (type === 'end-extsysops') {
      dispatch(endExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops', reason: value }))
    }
    closeConfirmModal()
  }

  useEffect(() => {
    if (monitor?.servicePlans) {
      setPlanOptions(getPlanOptions(monitor.servicePlans))
    }

    if (monitor?.partners) {
      setDbopsOptions(getPartnerOptions(monitor.partners, 'extdbops'))
      setPartnerOptions(getPartnerOptions(monitor.partners, 'extsysops'))
    }
  }, [monitor?.servicePlans, monitor?.partners])

  const dataObject = [
    ...(selectedCluster?.config?.cloud18
      ? [
        {
          key: 'For Sale',
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for cloud18-shared?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'cloud18-shared' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.cloud18Shared}
            />
          )
        },
        {
          key: 'Cluster Plan',
          value: (
            <Flex className={styles.dropdownContainer}>
              <Dropdown
                options={planOptions}
                id='plan'
                className={styles.dropdownButton}
                selectedValue={selectedCluster?.config?.provServicePlan}
                confirmTitle={`Confirm plan change to`}
                onChange={(option) => {
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'prov-service-plan',
                      value: option
                    })
                  )
                }}
              />
            </Flex>
          )
        },
        {
          key: 'Cloud18 Database Read-Write-Split Srv Record',
          value: (
            <TextForm
              value={selectedCluster?.config?.cloud18DatabaseReadWriteSplitSrvRecord}
              confirmTitle={`Confirm cloud18-database-read-write-split-srv-record to `}
              maxLength={1024}
              className={styles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'cloud18-database-read-write-split-srv-record',
                    value: value
                  })
                )
              }
            />
          )
        },
        {
          key: 'Cloud18 Database Read-Write Srv Record',
          value: (
            <TextForm
              value={selectedCluster?.config?.cloud18DatabaseReadWriteSrvRecord}
              confirmTitle={`Confirm cloud18-database-read-write-srv-record to `}
              maxLength={1024}
              className={styles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'cloud18-database-read-write-srv-record',
                    value: value
                  })
                )
              }
            />
          )
        },
        {
          key: 'Cloud18 Database Read Srv Record',
          value: (
            <TextForm
              value={selectedCluster?.config?.cloud18DatabaseReadSrvRecord}
              confirmTitle={`Confirm cloud18-database-read-srv-record to `}
              maxLength={1024}
              className={styles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'cloud18-database-read-srv-record',
                    value: value
                  })
                )
              }
            />
          )
        },
        {
          key: 'Cloud18 External Sys Ops',
          value: (
            <HStack w='100%'>
              <Flex className={styles.dropdownContainer}>
                <Dropdown
                  options={partnerOptions}
                  id='plan'
                  className={styles.dropdownButton}
                  selectedValue={selectedCluster?.config?.cloud18ExternalSysOps}
                  confirmTitle={`Confirm external Sys Ops change to`}
                  onChange={(option) => {
                    dispatch(
                      subscribeExternalRole({
                        clusterName: selectedCluster?.name,
                        username: option,
                        roles: 'extsysops'
                      })
                    )
                  }}
                />
              </Flex>
              {selectedCluster?.config?.cloud18ExternalSysOps && selectedCluster?.config?.cloud18ExternalSysOpsStatus == "pending" ? (
                <>
                  <RMIconButton tooltip={"Accept external sysops"} icon={TbDeviceDesktopStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "accept-extsysops", title: "Are you sure to accept external sysops?", payload: selectedCluster?.config?.cloud18ExternalSysOps }); openConfirmModal() }} />
                  <RMIconButton tooltip={"Reject external sysops"} icon={TbDeviceDesktopX} onClick={(e) => { e.stopPropagation(); setAction({ type: "reject-extsysops", title: "Are you sure to reject external sysops?", payload: selectedCluster?.config?.cloud18ExternalSysOps }); openTextModal() }} />
                </>
              ) : selectedCluster?.config?.cloud18ExternalDbOpsStatus == "active" ? (
                <RMIconButton tooltip={'End External Sysops Role'} icon={TbUserCancel} onClick={() => { e.stopPropagation(); setAction({ type: "end-extsysops", title: "Are you sure to end external sysops?", payload: selectedCluster?.config?.cloud18ExternalSysOps }); openTextModal() }} />
              ) : <></>
              }
            </HStack>
          )
        },
        {
          key: 'Cloud18 External DB Ops',
          value: (
            <HStack w='100%'>
              <Flex className={styles.dropdownContainer}>
                <Dropdown
                  options={dbopsOptions}
                  id='plan'
                  className={styles.dropdownButton}
                  selectedValue={selectedCluster?.config?.cloud18ExternalDbOps}
                  confirmTitle={`Confirm external DB Ops change to`}
                  onChange={(option) => {
                    dispatch(
                      subscribeExternalRole({
                        clusterName: selectedCluster?.name,
                        username: option,
                        roles: 'extdbops'
                      })
                    )
                  }}
                />
              </Flex>
              {selectedCluster?.config?.cloud18ExternalDbOps && selectedCluster?.config?.cloud18ExternalDbOpsStatus == "pending" ? (
                <>
                  <RMIconButton tooltip={"Accept external dbops"} icon={TbDatabaseStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "accept-extdbops", title: "Are you sure to accept external dbops?", payload: selectedCluster?.config?.cloud18ExternalDbOps }); openConfirmModal() }} />
                  <RMIconButton tooltip={"Reject external dbops"} icon={TbDatabaseX} onClick={(e) => { e.stopPropagation(); setAction({ type: "reject-extdbops", title: "Are you sure to reject external dbops?", payload: selectedCluster?.config?.cloud18ExternalDbOps }); openTextModal() }} />
                </>
              ) : selectedCluster?.config?.cloud18ExternalDbOpsStatus == "active" ? (
                <RMIconButton tooltip={'End External Role'} icon={TbUserCancel} onClick={() => { e.stopPropagation(); setAction({ type: "end-extdbops", title: "Are you sure to end external dbops?", payload: selectedCluster?.config?.cloud18ExternalDbOps }); openTextModal() }} />
              ) : <></>
              }
              <RMIconButton icon={HiKey} onClick={() => { setCredentialType('cloud18-dba-user-credentials'); setIsCredentialModalOpen(true) }} />
            </HStack>
          )
        },
        {
          key: 'Cloud18 Sponsor User Credentials',
          value: (
            <RMIconButton icon={HiKey} onClick={() => { setCredentialType('cloud18-sponsor-user-credentials'); setIsCredentialModalOpen(true) }} />
          )
        }
      ]
      : [])
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
      {isCredentialModalOpen && (
        <SetCredentialsModal
          clusterName={selectedCluster?.name}
          isOpen={isCredentialModalOpen}
          type={credentialType}
          closeModal={() => {
            setIsCredentialModalOpen(false)
            setCredentialType('')
          }}
        />
      )}
      {isConfirmModalOpen && <ConfirmModal title={title} isOpen={isConfirmModalOpen} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
      {isTextModalOpen && <TextInputModal isOpen={isTextModalOpen} title={title} fieldname={'Reason'} onSave={handleConfirm} isRequired={true} closeModal={() => { setIsTextModalOpen(false); }} />}

    </Flex>
  )
}

export default CloudSettings
