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
import { TbUserCancel } from 'react-icons/tb'
import { endExternalRole, subscribeExternalRole } from '../../redux/clusterSlice'
import TextInputModal from '../../components/Modals/TextInputModal'

function CloudSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  const [planOptions, setPlanOptions] = useState([])
  const [dbopsOptions, setDbopsOptions] = useState([])
  const [partnerOptions, setPartnerOptions] = useState([])
  const [credentialType, setCredentialType] = useState('')
  const [roleType, setRoleType] = useState('')
  const [isCredentialModalOpen, setIsCredentialModalOpen] = useState(false)
  const [isTextModalOpen, setIsTextModalOpen] = useState(false)

  const getPlanOptions = (plist = []) => [{ name: "No Plan", value: '' }, ...plist?.map((obj) => ({ name: obj.plan, value: obj.plan }))]
  const getPartnerOptions = (plist = [], role) => [{ name: "No Partner", value: '' }, ...plist?.map((obj) => ({ name: obj.Name, value: role === 'extdbops' ? obj.DbopsEmail : obj.SysopsEmail }))]

  const getExtRoleEmail = (role) => { 
    if (role === 'extdbops') {
      return selectedCluster?.config?.cloud18ExternalDbOps
    }
    
    return selectedCluster?.config?.cloud18ExternalSysOps
  }

  const handleEndExternalRole = (value) => { 
    dispatch(endExternalRole({ clusterName: selectedCluster?.name, username: getExtRoleEmail(roleType), roles: roleType, reason: value })) 
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
            <RMIconButton tooltip={'End External Sysops Role'} icon={TbUserCancel} onClick={() => { setRoleType('extsysops'); setIsTextModalOpen(true) }} />
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
            <RMIconButton tooltip={'End External Role'} icon={TbUserCancel} onClick={() => { setRoleType('extdbops'); setIsTextModalOpen(true) }} />
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
      {isTextModalOpen && (
        <TextInputModal
        key={roleType}
          isOpen={isTextModalOpen}
          title={'End External Role'}
          fieldname={'Reason'}
          onSave={handleEndExternalRole}
          isRequired={true}
          closeModal={() => {
            setIsTextModalOpen(false)
            setRoleType('')
          }}
        />
      )}
    </Flex>
  )
}

export default CloudSettings
