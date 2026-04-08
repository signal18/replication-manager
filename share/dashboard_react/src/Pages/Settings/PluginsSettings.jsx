import { Box, Flex, Spinner, Text } from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import { useState, useEffect } from 'react'
import PropTypes from 'prop-types'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import TableType2 from '../../components/TableType2'
import RMIconButton from '../../components/RMIconButton'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
import NumberInput from '../../components/NumberInput'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import { setGlobalSetting } from '../../redux/globalClustersSlice'
import styles from './styles.module.scss'
import { clusterService } from '../../services/clusterService'
import Dropdown from '../../components/Dropdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function PluginsSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const baseURL = useSelector((state) => state?.auth?.baseURL || '')
  const globalConfig = useSelector((state) => state?.globalClusters?.monitor?.config)
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      onClick={() => openInfoModal(title, content)}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
    />
  )

  const sw = (setting, configKey) => (
    <RMSwitch
      confirmTitle={`Confirm switch settings for ${setting}?`}
      onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))}
      isDisabled={user?.grants['cluster-settings'] == false}
      isChecked={selectedCluster?.config?.[configKey]}
    />
  )

  const hSigningKey = `**Plugin Signing Public Key**\n\nPath to the Ed25519 public key used to verify external log plugin binaries before they are executed.\n\nWhen set, every plugin binary pulled into the cluster plugins directory must have a matching \`.sig\` sidecar file in \`<share>/plugins/\`.\nPlugins that fail verification are rejected and logged as errors — they are never executed.\n\nLeave empty (or remove the key file) to skip verification on dev or unsigned builds.\n\nThe public key ships with the repman package at \`<ShareDir>/plugins/plugin-signing.pub\`.\n\nConfig: \`plugin-signing-public-key\``
  const hLogPlugin = `**Enable Log Plugins**\n\nGlobal switch that activates the external log plugin evaluation loop.\nWhen disabled no plugin binaries are executed regardless of individual plugin settings.\n\nConfig: \`log-plugin\``
  const hLogPluginLevel = `**Plugin Module Verbosity**\n\nLog verbosity level for the plugin evaluation subsystem.\n\n- **0** — disabled\n- **1** — errors only\n- **2** — errors + warnings\n- **3** — informational\n- **4** — debug\n\nConfig: \`log-level-plugin\``

  const [plugins, setPlugins] = useState([])
  const [pluginsLoading, setPluginsLoading] = useState(false)

  useEffect(() => {
    if (!selectedCluster?.name) return
    setPluginsLoading(true)
    clusterService
      .getClusterPlugins(selectedCluster.name, baseURL)
      .then(({ data }) => setPlugins(Array.isArray(data) ? data : []))
      .catch(() => setPlugins([]))
      .finally(() => setPluginsLoading(false))
  }, [selectedCluster?.name, selectedCluster?.config?.logPlugin, baseURL])

  const buildPluginRows = () => {
    const rows = []

    if (pluginsLoading) {
      rows.push({ key: 'Plugins', value: <Spinner size='sm' /> })
      return rows
    }

    if (plugins.length === 0) {
      rows.push({
        key: 'Plugins',
        value: <Text fontSize='sm' color='gray.400'>No plugins loaded — enable log-plugin and place plugin binaries in the cluster plugins directory</Text>
      })
      return rows
    }

    plugins.forEach((plugin) => {
      const isPluginEnabled = plugin.config?.['enabled'] !== 'false'
      rows.push({
        key: (
          <Text fontWeight='semibold' fontSize='sm' color='blue.300'>
            {plugin.name}
          </Text>
        ),
        value: (
          <RMSwitch
            confirmTitle={`Confirm ${isPluginEnabled ? 'disable' : 'enable'} plugin '${plugin.name}'?`}
            onChange={() =>
              dispatch(setSetting({
                clusterName: selectedCluster?.name,
                setting: `plugin-config-${plugin.name}-enabled`,
                value: isPluginEnabled ? 'false' : 'true'
              }))
            }
            isDisabled={user?.grants['cluster-settings'] == false}
            isChecked={isPluginEnabled}
          />
        )
      })

      pluginKnownKeys(plugin.name).forEach((key) => {
        const currentRaw = plugin.config?.[key] ?? pluginKeyDefault(plugin.name, key)

        let control
        if (key === 'min-log-level') {
          const levelOptions = [
            { value: 'System',  label: 'System — startup/shutdown only' },
            { value: 'Note',    label: 'Note — informational+' },
            { value: 'Warning', label: 'Warning — warnings + errors (default)' },
            { value: 'ERROR',   label: 'ERROR — errors only' },
          ]
          control = (
            <Dropdown
              options={levelOptions}
              selectedValue={currentRaw}
              isDisabled={user?.grants['cluster-settings'] == false}
              confirmTitle={`Confirm min-log-level for '${plugin.name}': `}
              onChange={(opt) =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-min-log-level`,
                  value: opt.value
                }))
              }
            />
          )
        } else {
          control = (
            <NumberInput
              min={key === 'spike-sigma' ? 0.5 : 1}
              max={key === 'spike-sigma' ? 10 : 8760}
              step={key === 'spike-sigma' ? 0.5 : 1}
              value={key === 'spike-sigma' ? parseFloat(currentRaw) : parseInt(currentRaw, 10)}
              isDisabled={user?.grants['cluster-settings'] == false}
              showConfirmModal={true}
              confirmTitle={`Confirm change '${key}' for '${plugin.name}' to: `}
              onConfirm={(val) =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-${key}`,
                  value: String(val)
                }))
              }
            />
          )
        }
        rows.push({
          key: `  ${pluginKeyLabel(plugin.name, key)}`,
          value: control
        })
      })
    })

    return rows
  }

  const dataObject = [
    {
      key: 'Signing',
      value: [
        {
          key: 'Plugin Signing Public Key',
          help: h(hSigningKey, 'Plugin Signing Public Key'),
          value: (
            <TextForm
              value={globalConfig?.pluginSigningPublicKey}
              confirmTitle={`Confirm change 'plugin-signing-public-key' to `}
              onSave={(value) =>
                dispatch(setGlobalSetting({ setting: 'plugin-signing-public-key', value: value.length === 0 ? '{undefined}' : value }))
              }
            />
          )
        }
      ]
    },
    {
      key: 'External Plugins',
      value: [
        {
          key: 'Enable Log Plugins',
          help: h(hLogPlugin, 'Enable Log Plugins'),
          value: sw('log-plugin', 'logPlugin')
        },
        {
          key: 'Plugin Module Verbosity',
          help: h(hLogPluginLevel, 'Plugin Module Verbosity'),
          value: (
            <LogSlider
              value={selectedCluster?.config?.logPluginLevel}
              confirmTitle={`Confirm change 'log-level-plugin' to: `}
              onChange={(val) =>
                dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-plugin', value: val }))
              }
            />
          )
        },
        ...buildPluginRows()
      ]
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

// ---- Plugin config metadata -------------------------------------------------

function pluginKnownKeys(pluginName) {
  switch (pluginName) {
    case 'errorlog':
      return ['timeframe-hours', 'min-log-level', 'spike-sigma']
    case 'sqlerrorlog':
    case 'slowlog':
      return ['timeframe-hours', 'spike-sigma']
    case 'auditlog':
      return ['current-window-hours', 'baseline-window-hours', 'spike-sigma']
    default:
      return ['spike-sigma']
  }
}

function pluginKeyLabel(_pluginName, key) {
  const labels = {
    'timeframe-hours': 'Timeframe (hours)',
    'current-window-hours': 'Current window (hours)',
    'baseline-window-hours': 'Baseline window (hours)',
    'spike-sigma': 'Spike threshold (σ)',
    'min-log-level': 'Min log level (errorlog only)'
  }
  return labels[key] || key
}

function pluginKeyDefault(pluginName, key) {
  if (pluginName === 'auditlog') {
    if (key === 'current-window-hours') return '1'
    if (key === 'baseline-window-hours') return '24'
  }
  if (key === 'spike-sigma') return '2'
  if (key === 'min-log-level') return 'Warning'
  return '24'
}

PluginsSettings.propTypes = {
  selectedCluster: PropTypes.object,
  user: PropTypes.object
}

export default PluginsSettings
