import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setGlobalSetting, reloadClustersPlan, reloadClustersPlanInfo } from '../../redux/globalClustersSlice'
import TextForm from '../../components/TextForm'
import RMIconButton from '../../components/RMIconButton'
import { HiOutlineInformationCircle, HiQuestionMarkCircle, HiRefresh } from 'react-icons/hi'
import RMButton from '../../components/RMButton'
import Markdown from 'react-markdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import remarkGfm from 'remark-gfm'
import ConfirmModal from '../../components/Modals/ConfirmModal'

function MarketplaceSettings({ config }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isInfoModalOpen, setIsInfoModalOpen] = useState(false)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmAction, setConfirmAction] = useState(null)
  const [shouldRedownload, setShouldRedownload] = useState(true)

  const openInfo = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsInfoModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      onClick={() => openInfo(title, content)}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
    />
  )

  const hPlatformDesc = `**Platform Description**\n\nHuman-readable description of this replication-manager platform shown to other Cloud18 users in the marketplace.\nHelps buyers and subscribers identify your offering and its purpose.\n\nConfig: \`cloud18-platform-description\``
  const hGatewayDomain = `**Gateway Domain Name**\n\nPublic FQDN for the Cloud18 API gateway that exposes this instance on the internet (e.g. \`repman.mycompany.cloud18.io\`).\nRequired for clusters accessible from the marketplace.\n\nConfig: \`cloud18-gateway-domain-name\``
  const hGatewayService = `**Gateway Service**\n\nOpenSVC service name of the Cloud18 gateway proxy.\nThe gateway routes inbound marketplace traffic to this replication-manager instance via the OpenSVC orchestrator.\n\nConfig: \`cloud18-gateway-service\``
  const hDomainAdd = `**Domain Add Script**\n\nShell script executed when a new marketplace subscription is activated.\nTypically creates DNS records and routing rules for the new tenant's domain.\n\nConfig: \`cloud18-domain-add-script\``
  const hDomainDrop = `**Domain Drop Script**\n\nShell script executed when a marketplace subscription is cancelled.\nShould remove the DNS entries and routing rules created by the Domain Add Script.\n\nConfig: \`cloud18-domain-drop-script\``
  const hDomainUser = `**Domain User**\n\nUsername for the domain management API (DNS provider, load balancer, etc.) called by the add/drop scripts to automate tenant routing.\n\nConfig: \`cloud18-domain-user\``
  const hDomainSecret = `**Domain Secret**\n\nAPI key or password for domain management authentication.\nStored encrypted in the replication-manager configuration.\n\nConfig: \`cloud18-domain-secret\``
  const hReloadPlans = `**Reload Plans**\n\nDownload and reapply marketplace service plans from the Cloud18 GitLab repository.\nPlans define available database topologies, resource profiles, and OpenSVC provisioning templates.\nUse the info button to reload plan metadata only without reprovisioning.`

  const dataObject = [
    {
      key: 'Platform Description',
      help: h(hPlatformDesc, 'Platform Description'),
      value: (
        <TextForm
          value={config?.cloud18PlatformDescription}
          confirmTitle='Confirm platform description to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-platform-description', value }))}
        />
      )
    },
    {
      key: 'Gateway Domain Name',
      help: h(hGatewayDomain, 'Gateway Domain Name'),
      value: (
        <TextForm
          value={config?.cloud18GatewayDomainName}
          confirmTitle='Confirm gateway domain name to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-gateway-domain-name', value }))}
        />
      )
    },
    {
      key: 'Gateway Service',
      help: h(hGatewayService, 'Gateway Service'),
      value: (
        <TextForm
          value={config?.cloud18GatewayService}
          confirmTitle='Confirm gateway service to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-gateway-service', value }))}
        />
      )
    },
    {
      key: 'Domain Add Script',
      help: h(hDomainAdd, 'Domain Add Script'),
      value: (
        <TextForm
          value={config?.cloud18DomainAddScript}
          confirmTitle='Confirm domain add script to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-add-script', value }))}
        />
      )
    },
    {
      key: 'Domain Drop Script',
      help: h(hDomainDrop, 'Domain Drop Script'),
      value: (
        <TextForm
          value={config?.cloud18DomainDropScript}
          confirmTitle='Confirm domain drop script to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-drop-script', value }))}
        />
      )
    },
    {
      key: 'Domain User',
      help: h(hDomainUser, 'Domain User'),
      value: (
        <TextForm
          value={config?.cloud18DomainUser}
          confirmTitle='Confirm domain user to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-user', value }))}
        />
      )
    },
    {
      key: 'Domain Secret',
      help: h(hDomainSecret, 'Domain Secret'),
      value: (
        <TextForm
          type='password'
          value={config?.cloud18DomainSecret}
          confirmTitle='Confirm domain secret to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-secret', value: btoa(value) }))}
        />
      )
    },
    {
      key: 'Reload Plans',
      help: h(hReloadPlans, 'Reload Plans'),
      value: (
        <Flex align='center' gap={2}>
          <RMIconButton
            icon={HiRefresh}
            tooltip='Reload plans (reapply)'
            aria-label='Reload all clusters plans'
            onClick={() => {
              setShouldRedownload(true)
              setConfirmAction({ type: 'reload-clusters-plan' })
              setIsConfirmModalOpen(true)
            }}
          />
          <RMIconButton
            icon={HiOutlineInformationCircle}
            tooltip='Reload plan info only'
            aria-label='Reload all clusters plan info'
            onClick={() => {
              setShouldRedownload(false)
              setConfirmAction({ type: 'reload-clusters-plan-info' })
              setIsConfirmModalOpen(true)
            }}
          />
        </Flex>
      )
    },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn />
      </Flex>
      <CommonModal
        isOpen={isInfoModalOpen}
        closeModal={() => setIsInfoModalOpen(false)}
        title={action.title}
        body={action.body}
        size='xl'
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => setIsConfirmModalOpen(false)}
          title={confirmAction?.type === 'reload-clusters-plan' ? 'Confirm reload all clusters plans?' : 'Confirm reload all clusters plan info?'}
          onConfirmClick={() => {
            if (confirmAction?.type === 'reload-clusters-plan') {
              dispatch(reloadClustersPlan({ download: shouldRedownload }))
            } else {
              dispatch(reloadClustersPlanInfo({ download: shouldRedownload }))
            }
            setIsConfirmModalOpen(false)
          }}
        />
      )}
    </>
  )
}

export default MarketplaceSettings
