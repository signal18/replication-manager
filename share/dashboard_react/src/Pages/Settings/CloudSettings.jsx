import { Flex, HStack } from '@chakra-ui/react'
import React, { useState, useEffect, useCallback } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import Dropdown from '../../components/Dropdown'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import SetCredentialsModal from '../../components/Modals/SetCredentialsModal'
import RMIconButton from '../../components/RMIconButton'
import { HiKey } from 'react-icons/hi'
import { TbDatabaseDollar, TbDatabaseOff, TbDatabaseStar, TbDatabaseX, TbDeviceDesktopDollar, TbDeviceDesktopOff, TbDeviceDesktopStar, TbDeviceDesktopX, TbUserCancel } from 'react-icons/tb'
import { acceptExternalRole, endExternalRole, quoteExternalRole, refuseExternalRole, subscribeExternalRole } from '../../redux/clusterSlice'
import TextInputModal from '../../components/Modals/TextInputModal'
import TermsModal from '../../components/Modals/TermsModal'
import NumberInputModal from '../../components/Modals/NumberInputModal'
import { getTermsData } from '../../redux/globalClustersSlice'

function CloudSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const {
    globalClusters: { monitor, terms },
    auth: { baseURL }
  } = useSelector((state) => state)

  const [action, setAction] = useState({ type: '', title: '', roles: '', payload: '' })
  const [planOptions, setPlanOptions] = useState([])
  const [dbopsOptions, setDbopsOptions] = useState([])
  const [partnerOptions, setPartnerOptions] = useState([])
  const [credentialType, setCredentialType] = useState('')
  const [isCredentialModalOpen, setIsCredentialModalOpen] = useState(false)
  const [isTermsModalOpen, setIsTermsModalOpen] = useState(null)
  const [isTextModalOpen, setIsTextModalOpen] = useState(false)
  const [isNumberModalOpen, setIsNumberModalOpen] = useState(false)
  const { type, title, roles, payload } = action

  const getPlanOptions = (plist = []) => [{ name: "No Plan", value: '' }, ...plist?.map((obj) => ({ name: obj.plan, value: obj.plan }))]
  const getPartnerOptions = (plist = [], role) => [{ name: "No Partner", value: '' }, ...plist?.map((obj) => ({ name: obj.Name, value: role === 'extdbops' ? obj.DbopsEmail : obj.SysopsEmail }))]

  const sysPartner = monitor?.partners?.find((partner) => partner.SysopsEmail === payload)
  const dbPartner = monitor?.partners?.find((partner) => partner.DbopsEmail === payload)
  const sysCost = selectedCluster?.config?.cloud18MonthlyExternalSysopsCost
  const dbCost = selectedCluster?.config?.cloud18MonthlyExternalDbopsCost

  const header = `
| Label | Value |
| --- | --- |
`

  const sysdetails = `
| Partner | ${sysPartner?.Name} |
| Role | External SysOps |
| Cost | ${sysCost} |
| Cluster | ${selectedCluster?.name} |`

  const dbdetails = `
| Partner | ${dbPartner?.Name} |
| Role | External DbOps |
| Cost | ${dbCost} |
| Cluster | ${selectedCluster?.name} |`

  const finalterms = terms
    .replace(`<<user>>`, user?.username || '')
    .replace(`<<cluster>>`, selectedCluster?.name || '')
    .replace(`<<ervice_plan_infos>>`, roles === 'extsysops' ? header.concat(sysdetails) : header.concat(dbdetails))
    .replace(`<<date>>`, (new Date()).toLocaleDateString())

  useEffect(() => {
    dispatch(getTermsData({ baseURL }))
  }, [selectedCluster?.name])
  useEffect(() => { }, [selectedCluster?.config?.cloud18ExternalSysOpsStatus, selectedCluster?.config?.cloud18ExternalDbOpsStatus])

  const openTermsModal = () => {
    setIsTermsModalOpen(true)
  }

  const closeTermsModal = () => {
    setIsTermsModalOpen(false)
  }

  const openTextModal = () => {
    setIsTextModalOpen(true)
  }

  const openNumberModal = () => {
    setIsNumberModalOpen(true)
  }

  const handleConfirm = (value) => {
    if (type === 'quote-extdbops') {
      dispatch(quoteExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops', cost: value }))
    } else if (type === 'quote-extsysops') {
      dispatch(quoteExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops', cost: value }))
    } else if (type === 'accept-extdbops') {
      dispatch(acceptExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops' }))
    } else if (type === 'accept-extsysops') {
      dispatch(acceptExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops' }))
    } else if (type === 'reject-extdbops') {
      dispatch(refuseExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops', reason: value }))
    } else if (type === 'reject-extsysops') {
      dispatch(refuseExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops', reason: value }))
    } else if (type === 'end-extdbops') {
      dispatch(endExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops', reason: value }))
    } else if (type === 'end-extsysops') {
      dispatch(endExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops', reason: value }))
    }
    closeTermsModal()
  }

  const hasDbops = selectedCluster?.config?.cloud18ExternalDbOps
  const dbopsPending = selectedCluster?.config?.cloud18ExternalDbOpsStatus == "pending"
  const dbopsActive = selectedCluster?.config?.cloud18ExternalDbOpsStatus == "active"
  const dbopsQuote = selectedCluster?.config?.cloud18ExternalDbOpsStatus == "quote"

  const hasSysops = selectedCluster?.config?.cloud18ExternalSysOps
  const sysopsPending = selectedCluster?.config?.cloud18ExternalSysOpsStatus == "pending"
  const sysopsActive = selectedCluster?.config?.cloud18ExternalSysOpsStatus == "active"
  const sysopsQuote = selectedCluster?.config?.cloud18ExternalSysOpsStatus == "quote"

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
              {hasSysops && sysopsPending && (<RMIconButton tooltip={"Quote external sysops"} icon={TbDeviceDesktopDollar} onClick={(e) => { e.stopPropagation(); setAction({ type: "quote-extsysops", title: "External Sys Ops quotation", roles: 'extsysops', payload: selectedCluster?.config?.cloud18ExternalSysOps }); openNumberModal() }} />)}
              {hasSysops && sysopsQuote && (<RMIconButton tooltip={"Accept external sysops"} icon={TbDeviceDesktopStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "accept-extsysops", title: "Are you sure to accept external sysops?", roles: 'extsysops', payload: selectedCluster?.config?.cloud18ExternalSysOps }); openTermsModal() }} />)}
              {hasSysops && !sysopsActive && (<RMIconButton tooltip={"Reject external sysops"} icon={TbDeviceDesktopX} onClick={(e) => { e.stopPropagation(); setAction({ type: "reject-extsysops", title: "Are you sure to reject external sysops?", roles: 'extsysops', payload: selectedCluster?.config?.cloud18ExternalSysOps }); openTextModal() }} />)}
              {hasSysops && sysopsActive && (<RMIconButton tooltip={'End External Role'} icon={TbDeviceDesktopOff} onClick={(e) => { e.stopPropagation(); setAction({ type: "end-extsysops", title: "Are you sure to end external sysops?", roles: 'extsysops', payload: selectedCluster?.config?.cloud18ExternalSysOps }); openTextModal() }} />)}
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
              {hasDbops && dbopsPending && (<RMIconButton tooltip={"Quote external dbops"} icon={TbDatabaseDollar} onClick={(e) => { e.stopPropagation(); setAction({ type: "quote-extdbops", title: "External DB Ops quotation", roles: 'extdbops', payload: selectedCluster?.config?.cloud18ExternalDbOps }); openNumberModal() }} />)}
              {hasDbops && dbopsQuote && (<RMIconButton tooltip={"Accept external dbops"} icon={TbDatabaseStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "accept-extdbops", title: "Are you sure to accept external dbops?", roles: 'extdbops', payload: selectedCluster?.config?.cloud18ExternalDbOps }); openTermsModal() }} />)}
              {hasDbops && !dbopsActive && (<RMIconButton tooltip={"Reject external dbops"} icon={TbDatabaseX} onClick={(e) => { e.stopPropagation(); setAction({ type: "reject-extdbops", title: "Are you sure to reject external dbops?", roles: 'extdbops', payload: selectedCluster?.config?.cloud18ExternalDbOps }); openTextModal() }} />)}
              {hasDbops && dbopsActive && (<RMIconButton tooltip={'End External Role'} icon={TbDatabaseOff} onClick={(e) => { e.stopPropagation(); setAction({ type: "end-extdbops", title: "Are you sure to end external dbops?", roles: 'extdbops', payload: selectedCluster?.config?.cloud18ExternalDbOps }); openTextModal() }} />)}
              <RMIconButton icon={HiKey} onClick={() => { setCredentialType('cloud18-dba-user-credentials'); setIsCredentialModalOpen(true) }} />
            </HStack>
          )
        },
        {
          key: 'Cloud18 Sponsor User Credentials',
          value: (
            <RMIconButton icon={HiKey} onClick={() => { setCredentialType('cloud18-sponsor-user-credentials'); setIsCredentialModalOpen(true) }} />
          )
        },
        {
          key: 'Cloud18 webhook alert channel',
          value: (
            <TextForm
              value={selectedCluster?.config?.cloud18AlertSlackChannel}
              confirmTitle={`Confirm cloud18 alert channel to `}
              className={styles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'cloud18-alert-slack-channel',
                    value: value
                  })
                )
              }
            />
          )
        },
        {
          key: 'Cloud18 webhook alert url',
          value: (
            <TextForm
              value={selectedCluster?.config?.cloud18AlertSlackUrl}
              confirmTitle={`Confirm cloud18 alert webhook url to `}
              className={styles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'cloud18-alert-slack-url',
                    value: value
                  })
                )
              }
            />
          )
        },
        {
          key: 'Cloud18 webhook alert user',
          value: (
            <TextForm
              value={selectedCluster?.config?.cloud18AlertSlackUser}
              confirmTitle={`Confirm slack user to `}
              className={styles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'cloud18-alert-slack-user',
                    value: value
                  })
                )
              }
            />
          )
        },
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
      {isTermsModalOpen && <TermsModal terms={finalterms} title={title} isOpen={isTermsModalOpen} onConfirmClick={handleConfirm} closeModal={closeTermsModal} />}
      {isTextModalOpen && <TextInputModal isOpen={isTextModalOpen} title={title} fieldname={'Reason'} onSave={handleConfirm} isRequired={true} closeModal={() => { setIsTextModalOpen(false); }} />}
      {isNumberModalOpen && <NumberInputModal isOpen={isNumberModalOpen} title={title} fieldname={'Cost'} onSave={handleConfirm} isRequired={true} closeModal={() => { setIsNumberModalOpen(false); }} />}
    </Flex>
  )
}

export default CloudSettings
