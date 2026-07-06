import { Box, Flex, HStack } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setGlobalSetting, switchGlobalSetting } from '../../redux/globalClustersSlice'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
import { TbApi } from 'react-icons/tb'
import RMIconButton from '../../components/RMIconButton'
import NumberInput from '../../components/NumberInput'
import InterventionModal from '../../components/Modals/InterventionModal'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { getApi } from '../../services/apiHelper'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'

function GlobalSettings({ config }) {
  const dispatch = useDispatch()
  const monitor = useSelector((state) => state?.globalClusters?.monitor)
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const [isInterventionModalOpen, setIsInterventionModalOpen] = useState(false)
  const [isEndInterventionModalOpen, setIsEndInterventionModalOpen] = useState(false)
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )

  const hBackupSlots = `**Concurrent Backup Slots**\n\nMaximum number of backups allowed to run at the same time across all clusters. Additional backup requests queue until a slot frees up.\n\nConfig: \`backup-concurrent-slots\``
  const hIntervention = `**Global Intervention**\n\nMutes alerting on every cluster while a maintenance intervention is in progress. Suppressed alerts are counted on the Mute pill and released when the intervention ends.`
  const hTokenTimeout = `**API Token Timeout**\n\nLifetime in hours of API session tokens (JWT). Users must log in again after expiry.\n\nConfig: \`api-token-timeout\``
  const hPublicUrl = `**API Public URL**\n\nPublic URL of this replication-manager instance, used in generated links (alerts, peer lists, marketplace).\n\nConfig: \`api-public-url\``
  const hMailFrom = `**Mail From**\n\nSender address used in alert emails.\nExample: \`replication-manager@company.com\`\n\nConfig: \`mail-from\``
  const hMailTo = `**Mail To**\n\nComma-separated list of recipient addresses for alert emails.\n\nConfig: \`mail-to\``
  const hMailSmtp = `**Mail SMTP Address**\n\nSMTP server in \`host:port\` format.\nExample: \`smtp.company.com:587\`\n\nConfig: \`mail-smtp-addr\``
  const hMailUser = `**Mail SMTP User**\n\nSMTP authentication username.\n\nConfig: \`mail-smtp-user\``
  const hMailPass = `**Mail SMTP Password**\n\nSMTP authentication password.\n\nConfig: \`mail-smtp-password\``
  const hMailTls = `**Mail SMTP TLS Skip Verify**\n\nSkips TLS certificate verification for the SMTP connection.\nOnly enable in controlled environments.\n\nConfig: \`mail-smtp-tls-skip-verify\``
  const hMailPool = `**Max Pool Connections**\n\nMaximum simultaneous SMTP connections in the mail pool.\n\nConfig: \`mail-max-pool\``
  const hMailTimeout = `**Mail Timeout**\n\nTimeout in seconds for SMTP operations. 0 disables the timeout.\n\nConfig: \`mail-timeout\``
  const hLogFile = `**Log File Level**\n\nVerbosity of the main log file: higher values include more detail up to debug.\n\nConfig: \`log-level-file\``
  const hLogGit = `**Log GIT**\n\nVerbosity of git config sync logging. Note: raising this level also shortens the git sync cadence.\n\nConfig: \`log-level-git\``
  const hLogSupport = `**Log Support**\n\nEnables the support log channel, shared with Cloud18 support when assistance is requested.\n\nConfig: \`log-support\``
  const hLogSupportLevel = `**Log Support Level**\n\nVerbosity of the support log channel.\n\nConfig: \`log-level-support\``
  const hLogStats = `**Log Stats Level**\n\nVerbosity of internal statistics logging.\n\nConfig: \`log-level-stats\``
  const hLogHeartbeat = `**Log HeartBeat**\n\nEnables logging of arbitration heartbeats exchanged with the arbitrator and peers.\n\nConfig: \`log-heartbeat\``
  const hLogHeartbeatLevel = `**Log HeartBeat Level**\n\nVerbosity of the heartbeat log channel.\n\nConfig: \`log-level-heartbeat\``
  const hSwagger = `**Enable API Swagger**\n\nServes the interactive API explorer at \`/api-docs/\`.\n\nConfig: \`api-swagger-enabled\``
  const hLogApiLogin = `**Log API Login**\n\nRecords every API login in the security log.\n\nConfig: \`monitoring-log-api-login\``
  const hLogApiSilent = `**Log API Login Silent Users**\n\nComma-separated users whose logins are not recorded (health probes, internal accounts).\n\nConfig: \`monitoring-log-api-login-silent-users\``

  useEffect(() => {
    // Re-render when the config prop changes
  }, [config]);

  const handleStartGlobalIntervention = ({ reason }) => {
    getApi(baseURL).post('actions/intervention-start', { reason })
      .then(() => setIsInterventionModalOpen(false))
      .catch((err) => console.error('Failed to start global intervention:', err))
  }

  const handleEndGlobalIntervention = () => {
    getApi(baseURL).post('actions/intervention-end')
      .then(() => setIsEndInterventionModalOpen(false))
      .catch((err) => console.error('Failed to end global intervention:', err))
  }

  const dataObject = [
    {
      key: 'Concurrent Backup Slots',
      help: h(hBackupSlots, 'Concurrent Backup Slots'),
      value: (
        <NumberInput
          min={0}
          value={config?.backupConcurrentSlots ?? 1}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change 'backup-concurrent-slots' to: `}
          onConfirm={(value) => dispatch(setGlobalSetting({ setting: 'backup-concurrent-slots', value: value }))}
        />
      )
    },
    {
      key: 'Global Intervention',
      help: h(hIntervention, 'Global Intervention'),
      value: (
        <RMSwitch
          isChecked={monitor?.isGlobalIntervention}
          confirmTitle={monitor?.isGlobalIntervention
            ? `End global intervention? (${monitor?.globalInterventionEntry?.reason || 'active'})`
            : undefined}
          onChange={() => {
            if (monitor?.isGlobalIntervention) {
              setIsEndInterventionModalOpen(true)
            } else {
              setIsInterventionModalOpen(true)
            }
          }}
        />
      )
    },
    {
      key: 'API Token Timeout in Hours',
      help: h(hTokenTimeout, 'API Token Timeout'),
      value: (
        <NumberInput
          min={1}
          value={config?.apiTokenTimeout}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change 'api-token-timeout' to: `}
          onConfirm={(value) => dispatch(setGlobalSetting({ setting: 'api-token-timeout', value: value }))}
        />
      )
    },
    {
      key: 'API Public URL',
      help: h(hPublicUrl, 'API Public URL'),
      value: (
        <TextForm
          value={config?.apiPublicUrl}
          confirmTitle={`Confirm API Public URL to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'api-public-url', value }))
          }}
        />
      )
    },
    {
      key: 'Mail From',
      help: h(hMailFrom, 'Mail From'),
      value: (
        <TextForm
          value={config?.mailFrom}
          confirmTitle={`Confirm mail-from to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'mail-from', value }))
          }}
        />
      )
    },
    {
      key: 'Mail To',
      help: h(hMailTo, 'Mail To'),
      value: (
        <TextForm
          value={config?.mailTo}
          confirmTitle={`Confirm mail-to to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'mail-to', value }))
          }}
        />
      )
    },
    {
      key: 'Mail SMTP Address',
      help: h(hMailSmtp, 'Mail SMTP Address'),
      value: (
        <TextForm
          value={config?.mailSmtpAddr}
          confirmTitle={`Confirm Mail SMTP Address to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'mail-smtp-addr', value }))
          }}
        />
      )
    },
    {
      key: 'Mail SMTP User',
      help: h(hMailUser, 'Mail SMTP User'),
      value: (
        <TextForm
          value={config?.mailSmtpUser}
          confirmTitle={`Confirm Mail SMTP User to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'mail-smtp-user', value }))
          }}
        />
      )
    },
    {
      key: 'Mail SMTP Password',
      help: h(hMailPass, 'Mail SMTP Password'),
      value: (
        <TextForm
          value={config?.mailSmtpPassword}
          confirmTitle={`Confirm Mail SMTP Password to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'mail-smtp-password', value: btoa(value) }))
          }}
        />
      )
    },
    {
      key: 'Mail SMTP TLS (Skip Verify)',
      help: h(hMailTls, 'Mail SMTP TLS'),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for Mail SMTP TLS?'}
          onChange={(_v, setRefresh) => dispatch(switchGlobalSetting({ setting: 'mail-smtp-tls-skip-verify', setRefresh }))}
          isChecked={config?.mailSmtpTlsSkipVerify}
        />
      )
    },
    {
      key: 'Max Pool Connections',
      help: h(hMailPool, 'Max Pool Connections'),
      value: (
        <NumberInput
          min={1}
          value={config?.mailMaxPool}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change 'mail-max-pool' to: `}
          onConfirm={(value) => dispatch(setGlobalSetting({ setting: 'mail-max-pool', value: value }))}
        />
      )
    },
    {
      key: 'Mail Timeout in Seconds (0 = no timeout)',
      help: h(hMailTimeout, 'Mail Timeout'),
      value: (
        <NumberInput
          min={0}
          value={config?.mailTimeout}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change 'mail-timeout' to: `}
          onConfirm={(value) => dispatch(setGlobalSetting({ setting: 'mail-timeout', value: value }))}
        />
      )
    },
    {
      key: 'Log File Level',
      help: h(hLogFile, 'Log File Level'),
      value: (
        <LogSlider
          value={config?.logFileLevel}
          confirmTitle={`Confirm change 'log-level-file' to: `}
          onChange={(val) =>
            dispatch(
              setGlobalSetting({
                setting: 'log-level-file',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Log GIT',
      help: h(hLogGit, 'Log GIT'),
      value: (
        <LogSlider
          value={config?.logGitLevel}
          confirmTitle={`Confirm change 'log-level-git' to: `}
          onChange={(val) =>
            dispatch(
              setGlobalSetting({
                setting: 'log-level-git',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Log Support',
      help: h(hLogSupport, 'Log Support'),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for Log Support?'}
          onChange={(_v, setRefresh) => dispatch(switchGlobalSetting({ setting: 'log-support', setRefresh }))}
          isChecked={config?.logSupport}
        />
      )
    },
    {
      key: 'Log Support Level',
      help: h(hLogSupportLevel, 'Log Support Level'),
      value: (
        <LogSlider
          value={config?.logSupportLevel}
          confirmTitle={`Confirm change 'log-level-support' to: `}
          onChange={(val) =>
            dispatch(
              setGlobalSetting({
                setting: 'log-level-support',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Log Stats Level',
      help: h(hLogStats, 'Log Stats Level'),
      value: (
        <LogSlider
          value={config?.logStatsLevel}
          confirmTitle={`Confirm change 'log-level-stats' to: `}
          onChange={(val) =>
            dispatch(
              setGlobalSetting({
                setting: 'log-level-stats',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Log HeartBeat',
      help: h(hLogHeartbeat, 'Log HeartBeat'),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for Log HeartBeat?'}
          onChange={(_v, setRefresh) => dispatch(switchGlobalSetting({ setting: 'log-heartbeat', setRefresh }))}
          isChecked={config?.logHeartbeat}
        />
      )
    },
    {
      key: 'Log HeartBeat Level',
      help: h(hLogHeartbeatLevel, 'Log HeartBeat Level'),
      value: (
        <LogSlider
          value={config?.logHeartbeatLevel}
          confirmTitle={`Confirm change 'log-level-heartbeat' to: `}
          onChange={(val) =>
            dispatch(
              setGlobalSetting({
                setting: 'log-level-heartbeat',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Enable API Swagger',
      help: h(hSwagger, 'Enable API Swagger'),
      value: (
        <HStack>
          <RMSwitch
            confirmTitle={'Confirm switch global settings for API Swagger?'}
            onChange={(_v, setRefresh) => dispatch(switchGlobalSetting({ setting: 'api-swagger-enabled', setRefresh }))}
            isChecked={config?.apiSwaggerEnabled}
          />
          { config?.apiSwaggerEnabled && (<RMIconButton icon={TbApi} onClick={() => { window.open("/api-docs/", "_blank") }} />) }
        </HStack>
      )
    },
    {
      key: 'Log API Login',
      help: h(hLogApiLogin, 'Log API Login'),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for Log API Login?'}
          onChange={(_v, setRefresh) => dispatch(switchGlobalSetting({ setting: 'monitoring-log-api-login', setRefresh }))}
          isChecked={config?.monitoringLogApiLogin}
        />
      )
    },
    {
      key: 'Log API Login Silent Users',
      help: h(hLogApiSilent, 'Log API Login Silent Users'),
      value: (
        <TextForm
          value={config?.monitoringLogApiLoginSilentUsers}
          confirmTitle={`Confirm change 'monitoring-log-api-login-silent-users' to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'monitoring-log-api-login-silent-users', value: value || '{undefined}' }))
          }}
        />
      )
    },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
      {isInterventionModalOpen && (
        <InterventionModal
          isOpen={isInterventionModalOpen}
          closeModal={() => setIsInterventionModalOpen(false)}
          onStart={handleStartGlobalIntervention}
        />
      )}
      {isEndInterventionModalOpen && (
        <ConfirmModal
          isOpen={isEndInterventionModalOpen}
          closeModal={() => setIsEndInterventionModalOpen(false)}
          title={`End global intervention? All clusters will resume notifications.`}
          onConfirmClick={handleEndGlobalIntervention}
        />
      )}
    </>
  )
}

export default GlobalSettings
