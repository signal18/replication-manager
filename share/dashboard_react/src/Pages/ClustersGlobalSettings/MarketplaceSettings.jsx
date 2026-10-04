import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setGlobalSetting, reloadClustersPlan, reloadClustersPlanInfo } from '../../redux/globalClustersSlice'
import TextForm from '../../components/TextForm'
import Dropdown from '../../components/Dropdown'
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

  const pricingMode = config?.cloud18MarketplacePricingMode || 'csv-service-plan'
  const isUnitPricing = pricingMode === 'global-unit-pricing'

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
  const hPricingMode = `**Marketplace Pricing Mode**\n\nHow clusters are priced in the Cloud18 marketplace:\n\n- **csv-service-plan** (default): each cluster is priced from a per-cluster service plan downloaded as CSV.\n- **global-unit-pricing**: all clusters are priced from a single global EUR price per Database Unit and per Application Unit — no per-cluster plan.\n\nConfig: \`cloud18-marketplace-pricing-mode\``
  const hDbuPrice = `**DBU Price**\n\nPrice in EUR per DBU, the Database Unit (1 core / 4 GB RAM / 40 GB disk / 1000 IOPS).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-dbu-price\``
  const hApuPrice = `**APU Price**\n\nPrice in EUR per APU, the Application Unit (its size is the setting \`resource-manager-ratio-apu\`, shown below) consumed by proxies and applications.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-apu-price\``

  const hBkuPrice = `**BKU Price**\n\nPrice of one Backup Unit per month, in Eur. One BKU is 20 GB of **local** storage on the cluster's NVMe pool: the last backup of each server, the restic archive when its repository is a local path, and the declared volumes of the cluster's applications times the agents holding a copy (a failover app replicates its volume on every agent), rounded up per app. Disk only: no cpu, no memory. It is counted against the cluster's BKU plan (\`prov-db-bku\`); the billed units are the plan or the usage rounded up to the next unit, whichever is larger. 0 = local backups are not priced.\n\nConfig: \`cloud18-marketplace-bku-price\``
  const hBauPrice = `**BAU Price**\n\nPrice of one Backup Archive Unit per month, in Eur. One BAU is 20 GB of **archive** storage, two roles: as consumer, what restic holds off the cluster on S3 or SFTP, after deduplication; as producer, the declared volume of a storage application (*S3 provider* in the app settings, e.g. minio) that hosts an archive for others. There is no plan: billed on usage, rounded up to the next unit. The price applies to Signal18 or partner storage only; a cluster that brought its own remote storage (*Cloud18 → Remote archive on client storage*) is tracked but never priced. 0 = the remote archive is not priced.\n\nConfig: \`cloud18-marketplace-bau-price\``
  const hGatewayBandwidth = `**Gateway Bandwidth (Mb/s)**\n\nUplink capacity of each Cloud18 gateway in Mb/s, comma-separated and aligned with the gateway services (one value applies to all; 1000 by default). The traffic of every cluster through the gateways is tracked in Mb/s against it on the Resource Manager page (stacked per cluster): when the stack reaches the capacity the shared uplink saturates. More bandwidth = another gateway with its own VIP, stick tables shared and DNS round robin. Not invoiced.\n\nConfig: \`cloud18-gateway-bandwidth-mbit\``
  const hGwuPrice = `**GWU Price**\n\nPrice of one Gateway Unit per month, in Eur. One GWU is \`cloud18-marketplace-gwu-unit-mb\` MB (million octets, 100 by default) exchanged (**in and out**) through the Cloud18 gateways by the cluster's applications, read on every gateway's HAProxy stats port and attributed by backend name. Counted against the cluster's plan (\`prov-gateway-units\`, 10 by default) with the over and under-commit percentages; the family is a volume, the month-to-date total is billed, not a rate. 0 = gateway traffic is tracked (Resource Manager page, bandwidth per cluster) but not invoiced and absent from the statement.\n\nConfig: \`cloud18-marketplace-gwu-price\``
  const hGwuVolume = `**GWU Traffic Size (MB)**\n\nMB of traffic (in + out, million octets) per GWU in the monthly statement reported to the back office. 100 by default.\n\nConfig: \`cloud18-marketplace-gwu-unit-mb\``
  const hGwuFree = `**Free GWU per cluster**\n\nGWU of traffic every cluster gets free each month (10 by default = 1 GB). The traffic on top is reported as borrowed. A cluster file can override it.\n\nConfig: \`cloud18-marketplace-gwu-free-units\``
  const hGwuUnit = `**GWU Size (MB)**\n\nOctets (in + out) per GWU, in MB (million octets). Changing it changes every cluster's unit count at the next poll.\n\nConfig: \`cloud18-marketplace-gwu-unit-mb\``

  const hOverPct = `**Over-commit Price Ratio**\n\nSurcharge on a unit consumed **above** the plan, in percent of the unit price. 150 means an over-plan unit costs 2.5 times the unit price. Applies to every unit family with a plan (DBU, APU, BKU); the BAU has no plan and is pure usage, so it is never marked up. Global to this replication-manager instance.\n\nConfig: \`cloud18-marketplace-overcommit-price-pct\` (default 150)`
  const hUnderPct = `**Under-commit Price Ratio**\n\nReduction on a plan unit left **unconsumed**, in percent of the unit price. 80 means an unused plan unit costs 0.2 times the unit price; 0 bills the plan in full whatever is consumed. The pendant of the over-commit ratio, asymmetric on purpose. Global to this replication-manager instance.\n\nConfig: \`cloud18-marketplace-undercommit-price-pct\` (default 80)`

  const hRatio = (unit, key) => `**${unit} ratio**\n\nWhat one ${unit} is made of, as \`cores=…,mem=…,disk=…,iops=…\` (mem in m or g, disk in g or t; a missing key excludes the axis). This is the ONE source of the ratio: the resource manager, the billing, the charts and the configurators all read it. Changing it re-projects every plan and consumption at the next tick; it does not resize anything.\n\nConfig: \`${key}\``

  const dataObject = [
    {
      key: 'DBU ratio',
      help: h(hRatio('DBU', 'resource-manager-ratio-dbu'), 'DBU ratio'),
      value: (<TextForm value={config?.resourceManagerRatioDbu} confirmTitle='Confirm DBU ratio to ' onSave={(value) => dispatch(setGlobalSetting({ setting: 'resource-manager-ratio-dbu', value }))} />)
    },
    {
      key: 'APU ratio',
      help: h(hRatio('APU', 'resource-manager-ratio-apu'), 'APU ratio'),
      value: (<TextForm value={config?.resourceManagerRatioApu} confirmTitle='Confirm APU ratio to ' onSave={(value) => dispatch(setGlobalSetting({ setting: 'resource-manager-ratio-apu', value }))} />)
    },
    {
      key: 'BKU / BAU ratio',
      help: h(hRatio('BKU', 'resource-manager-ratio-bku'), 'BKU ratio'),
      value: (<TextForm value={config?.resourceManagerRatioBku} confirmTitle='Confirm BKU ratio to ' onSave={(value) => dispatch(setGlobalSetting({ setting: 'resource-manager-ratio-bku', value }))} />)
    },
    {
      key: 'Marketplace Pricing Mode',
      help: h(hPricingMode, 'Marketplace Pricing Mode'),
      value: (
        <Dropdown
          options={[
            { value: 'csv-service-plan', label: 'CSV Service Plan (per-cluster)' },
            { value: 'global-unit-pricing', label: 'Global Unit Pricing (EUR / DBU + APU)' }
          ]}
          selectedValue={pricingMode}
          confirmTitle='Confirm marketplace pricing mode: '
          onChange={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-pricing-mode', value }))}
        />
      )
    },
    ...(isUnitPricing ? [
      {
        key: 'DBU Price (EUR)',
        help: h(hDbuPrice, 'DBU Price'),
        value: (
          <TextForm
            value={config?.cloud18MarketplaceDbuPrice}
            regexPattern='^\d+(\.\d+)?$'
            confirmTitle='Confirm DBU price (EUR) to '
            onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-dbu-price', value }))}
          />
        )
      },
      {
        key: 'APU Price (EUR)',
        help: h(hApuPrice, 'APU Price'),
        value: (
          <TextForm
            value={config?.cloud18MarketplaceApuPrice}
            regexPattern='^\d+(\.\d+)?$'
            confirmTitle='Confirm APU price (EUR) to '
            onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-apu-price', value }))}
          />
        )
      },
    ] : []),
    {
      key: 'BKU Price (Eur / month)',
      help: h(hBkuPrice, 'BKU Price'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceBkuPrice ?? '')}
          type='number'
          confirmTitle='Confirm BKU price (Eur per backup unit per month) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-bku-price', value }))}
        />
      )
    },
    {
      key: 'BAU Price (Eur / month)',
      help: h(hBauPrice, 'BAU Price'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceBauPrice ?? '')}
          type='number'
          confirmTitle='Confirm BAU price (Eur per backup archive unit per month) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-bau-price', value }))}
        />
      )
    },
    {
      key: 'GWU Price (Eur / month)',
      help: h(hGwuPrice, 'GWU Price'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceGwuPrice ?? '')}
          type='number'
          confirmTitle='Confirm GWU price (Eur per gateway unit per month) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-gwu-price', value }))}
        />
      )
    },
    {
      key: 'GWU Traffic Size (MB per unit)',
      help: h(hGwuVolume, 'GWU Traffic Size'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceGwuUnitMb ?? '')}
          type='number'
          confirmTitle='Confirm the GWU traffic size in MB (million octets) per unit to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-gwu-unit-mb', value }))}
        />
      )
    },
    {
      key: 'Free GWU per cluster (per month)',
      help: h(hGwuFree, 'Free GWU'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceGwuFreeUnits ?? '')}
          type='number'
          confirmTitle='Confirm the free GWU of traffic per cluster and per month to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-gwu-free-units', value }))}
        />
      )
    },
    {
      key: 'GWU Bandwidth Size (Mb/s per unit, Resource Manager axis)',
      help: h(hGwuUnit, 'GWU Size'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceGwuUnitMbit ?? '')}
          type='number'
          confirmTitle='Confirm the GWU size in Mb/s per unit to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-gwu-unit-mbit', value }))}
        />
      )
    },
    {
      key: 'Over-commit Surcharge (%)',
      help: h(hOverPct, 'Over-commit Price Ratio'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceOvercommitPricePct ?? '')}
          type='number'
          confirmTitle='Confirm over-commit price ratio (% of the unit price) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-overcommit-price-pct', value }))}
        />
      )
    },
    {
      key: 'Under-commit Reduction (%)',
      help: h(hUnderPct, 'Under-commit Price Ratio'),
      value: (
        <TextForm
          value={String(config?.cloud18MarketplaceUndercommitPricePct ?? '')}
          type='number'
          confirmTitle='Confirm under-commit price ratio (% of the unit price) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-undercommit-price-pct', value }))}
        />
      )
    },
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
      key: 'Gateway Bandwidth (Mb/s)',
      help: h(hGatewayBandwidth, 'Gateway Bandwidth'),
      value: (
        <TextForm
          value={String(config?.cloud18GatewayBandwidthMbit ?? '')}
          placeholder='1000'
          confirmTitle='Confirm the gateway uplink capacity in Mb/s (one value per gateway, comma-separated) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-gateway-bandwidth-mbit', value }))}
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
    ...(!isUnitPricing ? [{
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
    }] : []),
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
