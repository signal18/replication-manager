import { Flex, HStack, Link } from '@chakra-ui/react'
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

function GlobalSettings({ config }) {
  const dispatch = useDispatch()
  const monitor = useSelector((state) => state?.globalClusters?.monitor)
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const [isInterventionModalOpen, setIsInterventionModalOpen] = useState(false)
  const [isEndInterventionModalOpen, setIsEndInterventionModalOpen] = useState(false)

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
      key: 'Global Intervention',
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
      key: 'Enable API Swagger',
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
        <TableType2 dataArray={dataObject} className={styles.table} />
      </Flex>
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
