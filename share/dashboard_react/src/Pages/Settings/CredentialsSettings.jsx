import { Badge, Box, Flex, Text, Tooltip } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import RMButton from '../../components/RMButton'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import Dropdown from '../../components/Dropdown'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import { reloadCertificates, rotateCertificates, rotateDBCredential } from '../../redux/clusterSlice'
import { convertObjectToArrayForDropdown } from '../../utility/common'
import SetCredentialsModal from '../../components/Modals/SetCredentialsModal'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import SecretKvEntries from './SecretKvEntries'

// where a credential's value comes from (cluster JSON `credentials`, #2050, #2227)
const SOURCE_LABEL = {
  versions: { label: 'Versioned stores', color: 'blue' },
  'config-file': { label: 'Config file (immutable)', color: 'orange' },
  'dynamic-config': { label: 'Dynamic config', color: 'green' },
  'runtime-config': { label: 'Runtime only (monitoring-save-config off)', color: 'yellow' },
  default: { label: 'Default', color: 'gray' },
  vault: { label: 'Vault', color: 'purple' }
}

// a button greyed out with the reason on hover
const GatedButton = ({ reason, isDisabled, onClick, children }) => (
  <Tooltip label={reason} isDisabled={!reason} hasArrow shouldWrapChildren>
    <RMButton isDisabled={isDisabled || !!reason} onClick={onClick}>
      {children}
    </RMButton>
  </Tooltip>
)

function CredentialsSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const [credentialType, setCredentialType] = useState('')
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [sslModes, setSslModes] = useState([])

  useEffect(() => {
    if (monitor?.serviceSslMode) {
      setSslModes([{ label: 'Default', value: '' }, ...convertObjectToArrayForDropdown(monitor.serviceSslMode)])
    }
  }, [monitor?.serviceSslMode])

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }
  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />

  const g = user?.grants || {}
  const config = selectedCluster?.config || {}
  const sw = (setting, isChecked, isDisabled = false) => (
    <RMSwitch
      confirmTitle={`Confirm switch settings for ${setting}?`}
      onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))}
      isDisabled={g['cluster-settings'] == false || isDisabled}
      isChecked={isChecked}
    />
  )

  const credential = (key) => (selectedCluster?.credentials || []).find((c) => c.key === key)
  const credentialRow = (key) => {
    const c = credential(key)
    const source = SOURCE_LABEL[c?.source]
    const initial = SOURCE_LABEL[c?.initial]
    return (
      <Flex align='center' gap='3' wrap='wrap'>
        <Text as='span' fontWeight='semibold'>{c?.user || '—'}</Text>
        <Text as='span' color='gray.500'>password ****</Text>
        {source && <Tooltip label='where the current value is read from' hasArrow><Badge colorScheme={source.color}>current: {source.label}</Badge></Tooltip>}
        {initial && <Tooltip label='the starting value, kept by the config forever' hasArrow><Badge variant='outline'>initial: {initial.label}</Badge></Tooltip>}
        <Text as='span' fontSize='sm' color='gray.500'>
          {c?.versions ? `${c.versions} version${c.versions > 1 ? 's' : ''}` : 'no version yet'}
          {c?.lastChange && !c.lastChange.startsWith('0001') ? `, last ${new Date(c.lastChange).toLocaleString()}` : ''}
        </Text>
        <GatedButton reason={c?.refusal ? `Refused: ${c.refusal}` : ''} isDisabled={!g['cluster-settings']} onClick={() => setCredentialType(key)}>
          Change
        </GatedButton>
      </Flex>
    )
  }

  const versioningOn = config.monitoringSecretVersioning || config.backupEncryption
  const vaultOn = (config.secretKvs || []).some((e) => e.enabled)
  // a change or a rotation needs an active store that keeps versions (#2227 rule 9)
  const storesMissing = !vaultOn && !versioningOn
  // what a store is for, under its switch
  const role = (text) => <Text as='span' fontSize='sm' color='gray.500'>{text}</Text>

  const hMonitoring = `**Monitoring User**\n\nThe account replication-manager uses to monitor and manage the databases.\n\n- **initial**: the starting value, kept by the config forever (a file under /etc, the cluster's dynamic config, or the default).\n- **current**: from the first change or rotation, the newest version of the versioned stores (the local file, the vaults); before it, the initial value.\n\nA change is a new version in every active store, refused when none keeps versions or when the value is set in a file under /etc. It applies to every database at once. Config: \`db-servers-credential\``
  const hReplication = `**Replication User**\n\nThe account the replicas use to connect to the primary. Same sources and refusals as the monitoring user.\n\nConfig: \`replication-credential\``
  const hRotate = `**Rotate Passwords**\n\nGenerates new passwords for the monitoring and replication users and sets them on the databases (with a Vault database engine, Vault rotates them).\n\nIt needs an active store that keeps the versions, **Local encrypted** on or a vault on, and neither user set in a config file under /etc. Otherwise a restart would lose access to the databases.`
  const hTls = `**Replication Use TLS**\n\nReplicas connect to the primary over TLS.\n\nConfig: \`replication-use-ssl\``
  const hSslMode = `**Database TLS Mode**\n\nThe TLS mode replication-manager uses to connect to the databases. Default (empty) uses TLS when available, and VERIFY_CA when the ssl tag is set in the configurator.\n\nConfig: \`db-servers-tls-ssl-mode\``
  const hCerts = `**Certificates**\n\n**Rotate** generates new certificates and deploys them. **Reload** makes the databases and replication-manager read the current ones again.`
  const hDynamic = `**Store: Dynamic Config**\n\nThe cluster's settings are saved in its config file. For the database credentials it keeps the initial value only: the current one and its versions live in the versioned stores.\n\nConfig: \`monitoring-save-config\``
  const hLocal = `**Store: Local Encrypted**\n\nKeeps every version of the credentials, encrypted, in secret_store.json, written at each save and pushed with the config; read at start, its newest version is the current value. Also on when backup encryption is on.\n\nConfig: \`monitoring-secret-versioning\``
  const hVault = `**Store: Vaults**\n\nEach vault that is on gets every version of every secret of the cluster at mount/path-prefix/cluster/databases, proxies, apps or archives/name/key. The newest version across the stores is the current one. A vault behind the others is flagged and gets the newest version at the next pass. The cluster's sponsor can turn a vault on or off. Turning one off keeps what it holds.\n\nConfig: \`[[secret-kv]]\``

  const dataObject = [
    { key: 'Monitoring User', help: h(hMonitoring, 'Monitoring User'), value: credentialRow('db-servers-credential') },
    { key: 'Replication User', help: h(hReplication, 'Replication User'), value: credentialRow('replication-credential') },
    {
      key: 'Rotate Passwords',
      help: h(hRotate, 'Rotate Passwords'),
      value: (
        <Flex align='center' gap='3' wrap='wrap'>
          <GatedButton
            reason={selectedCluster?.rotatePasswordsRefusal ? `Refused: ${selectedCluster.rotatePasswordsRefusal}` : ''}
            isDisabled={!g['cluster-rotate-passwords']}
            onClick={() => openConfirmModal('Rotate the database passwords?', () => () => dispatch(rotateDBCredential({ clusterName: selectedCluster?.name })))}>
            Rotate
          </GatedButton>
          {storesMissing && role('needs Local Encrypted on, or a vault on')}
        </Flex>
      )
    },
    { key: 'Replication Use TLS', help: h(hTls, 'Replication Use TLS'), value: sw('replication-use-ssl', config.replicationUseSsl) },
    {
      key: 'Database TLS Mode',
      help: h(hSslMode, 'Database TLS Mode'),
      value: (
        <Dropdown
          className={styles.dropdownButton}
          options={sslModes}
          selectedValue={config.dbServersTlsSslMode}
          confirmTitle='Confirm change database TLS mode to '
          placeholder='TLS mode'
          isDisabled={g['cluster-settings'] == false}
          onChange={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'db-servers-tls-ssl-mode', value: value === undefined ? '' : value }))}
        />
      )
    },
    {
      key: 'Certificates',
      help: h(hCerts, 'Certificates'),
      value: (
        <Flex gap='2'>
          <RMButton isDisabled={!g['cluster-certificates-rotate']} onClick={() => openConfirmModal('Rotate certificates?', () => () => dispatch(rotateCertificates({ clusterName: selectedCluster?.name })))}>
            Rotate
          </RMButton>
          <RMButton isDisabled={!g['cluster-certificates-reload']} onClick={() => openConfirmModal('Reload certificates?', () => () => dispatch(reloadCertificates({ clusterName: selectedCluster?.name })))}>
            Reload
          </RMButton>
        </Flex>
      )
    },
    {
      key: 'Store: Dynamic Config',
      help: h(hDynamic, 'Store: Dynamic Config'),
      value: (
        <Flex align='center' gap='3' wrap='wrap'>
          {sw('monitoring-save-config', config.monitoringSaveConfig)}
          {role('the initial values')}
        </Flex>
      )
    },
    {
      key: 'Store: Local Encrypted',
      help: h(hLocal, 'Store: Local Encrypted'),
      value: (
        <Flex align='center' gap='3' wrap='wrap'>
          {sw('monitoring-secret-versioning', versioningOn, !!config.backupEncryption)}
          {role(config.backupEncryption ? 'every version (history), on through backup encryption' : 'every version (history)')}
        </Flex>
      )
    },
    {
      key: 'Store: Vaults',
      help: h(hVault, 'Store: Vaults'),
      value: <SecretKvEntries clusterName={selectedCluster?.name} user={user} />
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      {credentialType && (
        <SetCredentialsModal clusterName={selectedCluster?.name} isOpen={!!credentialType} type={credentialType} closeModal={() => setCredentialType('')} />
      )}
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default CredentialsSettings
