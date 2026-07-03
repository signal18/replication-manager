import { Button, Flex, Text, useToast } from '@chakra-ui/react'
import React from 'react'
import { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setGlobalSetting, switchGlobalSetting } from '../../redux/globalClustersSlice'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
import NumberInput from '../../components/NumberInput'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { getApi } from '../../services/apiHelper'

// Plans allowed to use external arbitration; must mirror
// config.Config.IsEligibleForArbitration() on the server side.
const ARBITRATION_PLANS = ['support', 'support-services', 'partner']

function ArbitrationSettings({ config }) {
  const dispatch = useDispatch()
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const [isForgetArbModalOpen, setIsForgetArbModalOpen] = useState(false)
  const toast = useToast()

  const plan = (config?.cloud18SubscriptionPlan || '').trim().toLowerCase()
  const isEligible = !!config?.cloud18GitUser && ARBITRATION_PLANS.includes(plan)

  const handleForgetArbitration = () => {
    getApi(baseURL).post('actions/forget-arbitration')
      .then(() => {
        setIsForgetArbModalOpen(false)
        toast({ title: 'Arbitration data reset', status: 'success', duration: 3000 })
      })
      .catch((err) => {
        setIsForgetArbModalOpen(false)
        toast({ title: 'Failed to reset arbitration', description: err?.message, status: 'error', duration: 5000 })
      })
  }

  const dataObject = [
    ...(!isEligible
      ? [
          {
            key: 'Subscription Required',
            value: (
              <Text fontSize='sm'>
                External arbitration requires a registered Cloud18 account with a support or partner subscription plan
                (current plan: {plan || 'unregistered'}). Register in the Registration section above.
              </Text>
            )
          }
        ]
      : []),
    {
      key: 'External Arbitration',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for External Arbitration?'}
          onChange={(_v, setRefresh) => dispatch(switchGlobalSetting({ setting: 'arbitration-external', setRefresh }))}
          isChecked={config?.arbitrationExternal}
          isDisabled={!isEligible && !config?.arbitrationExternal}
        />
      )
    },
    {
      key: 'Arbitrator Address',
      value: (
        <TextForm
          value={config?.arbitrationExternalHosts}
          confirmTitle={`Confirm arbitration-external-hosts to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'arbitration-external-hosts', value }))
          }}
        />
      )
    },
    {
      key: 'Arbitration Secret',
      value: (
        <TextForm
          type='password'
          value={config?.arbitrationExternalSecret}
          confirmTitle={`Confirm Arbitration Secret to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'arbitration-external-secret', value: btoa(value) }))
          }}
        />
      )
    },
    {
      key: 'Unique Instance Id',
      value: (
        <NumberInput
          min={0}
          value={config?.arbitrationExternalUniqueId ?? 0}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change 'arbitration-external-unique-id' to: `}
          onConfirm={(value) => dispatch(setGlobalSetting({ setting: 'arbitration-external-unique-id', value: value }))}
        />
      )
    },
    {
      key: 'Peer Replication-Manager Hosts',
      value: (
        <TextForm
          value={config?.arbitrationPeerHosts}
          confirmTitle={`Confirm arbitration-peer-hosts to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'arbitration-peer-hosts', value }))
          }}
        />
      )
    },
    {
      key: 'Failed Master Script',
      value: (
        <TextForm
          value={config?.arbitrationFailedMasterScript}
          confirmTitle={`Confirm arbitration-failed-master-script to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'arbitration-failed-master-script', value }))
          }}
        />
      )
    },
    {
      key: 'Read Timeout in Milliseconds',
      value: (
        <NumberInput
          min={0}
          value={config?.arbitrationReadTimout ?? 800}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change 'arbitration-read-timeout' to: `}
          onConfirm={(value) => dispatch(setGlobalSetting({ setting: 'arbitration-read-timeout', value: value }))}
        />
      )
    },
    {
      key: 'Reset Arbitration',
      value: (
        <Button size='sm' colorScheme='red' variant='outline' onClick={() => setIsForgetArbModalOpen(true)}>
          Reset
        </Button>
      )
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} />
      </Flex>
      {isForgetArbModalOpen && (
        <ConfirmModal
          isOpen={isForgetArbModalOpen}
          closeModal={() => setIsForgetArbModalOpen(false)}
          title='Reset all arbitration data? This deletes all heartbeat rows from the arbitrator. A new election will run on the next monitoring tick.'
          onConfirmClick={handleForgetArbitration}
        />
      )}
    </>
  )
}

export default ArbitrationSettings
