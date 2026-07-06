import { Box, Button, Flex, Text, useToast } from '@chakra-ui/react'
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
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import { getApi } from '../../services/apiHelper'

// Plans allowed to use external arbitration; must mirror
// config.Config.IsEligibleForArbitration() on the server side.
const ARBITRATION_PLANS = ['support', 'support-services', 'partner']

function ArbitrationSettings({ config }) {
  const dispatch = useDispatch()
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const [isForgetArbModalOpen, setIsForgetArbModalOpen] = useState(false)
  const toast = useToast()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )

  const hExternal = `**External Arbitration**\n\nEnables split-brain protection through the external arbitrator service. During a network partition each peer replication-manager asks the arbitrator which side holds the majority; per cluster, the losing side goes passive so two masters can never be promoted.\n\nRequires a registered Cloud18 account with a support, support-services or partner plan.\n\nConfig: \`arbitration-external\``
  const hHosts = `**Arbitrator Address**\n\nURL of the external arbitrator service.\nDefault: \`https://arbitrator.cloud18.io\`\n\nConfig: \`arbitration-external-hosts\``
  const hSecret = `**Arbitration Secret**\n\nShared identifier registering this pair of instances on the arbitrator. It never leaves the server in clear text and acts as a primary key on the arbitrator side — both peers must hold the same value (exchanged through the config sync).\n\nConfig: \`arbitration-external-secret\``
  const hUniqueId = `**Unique Instance Id**\n\nIdentity of this instance among its peers (1, 2, ...). Peers MUST use different ids: the id also names this instance's GitLab access token and its config event log (\`event-changed.<id>.log\`).\n\nConfig: \`arbitration-external-unique-id\``
  const hPeerHosts = `**Peer Replication-Manager Hosts**\n\nComma-separated API addresses of the peer replication-manager instances, probed during split-brain detection.\n\nConfig: \`arbitration-peer-hosts\``
  const hFailedScript = `**Failed Master Script**\n\nScript executed when arbitration declares a master failed. Receives the failed master host and port as arguments.\n\nConfig: \`arbitration-failed-master-script\``
  const hReadTimeout = `**Read Timeout**\n\nTimeout in milliseconds for arbitrator responses during elections.\n\nConfig: \`arbitration-read-timeout\``
  const hReset = `**Reset Arbitration**\n\nDeletes every heartbeat row registered on the arbitrator for this secret. A fresh election runs on the next monitoring tick.\nUse after changing unique ids or after testing.`

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
      help: h(hExternal, 'External Arbitration'),
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
      help: h(hHosts, 'Arbitrator Address'),
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
      help: h(hSecret, 'Arbitration Secret'),
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
      help: h(hUniqueId, 'Unique Instance Id'),
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
      help: h(hPeerHosts, 'Peer Replication-Manager Hosts'),
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
      help: h(hFailedScript, 'Failed Master Script'),
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
      help: h(hReadTimeout, 'Read Timeout'),
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
      help: h(hReset, 'Reset Arbitration'),
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
        <TableType2 dataArray={dataObject} className={styles.table} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
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
