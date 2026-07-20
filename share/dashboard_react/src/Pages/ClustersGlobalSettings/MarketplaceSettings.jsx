import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setGlobalSetting, reloadClustersPlan, reloadClustersPlanInfo, recalculateMarketplaceUnits } from '../../redux/globalClustersSlice'
import TextForm from '../../components/TextForm'
import Dropdown from '../../components/Dropdown'
import RMIconButton from '../../components/RMIconButton'
import { HiCalculator, HiOutlineInformationCircle, HiQuestionMarkCircle, HiRefresh } from 'react-icons/hi'
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
  const hPricingMode = `**Marketplace Pricing Mode**\n\nHow clusters are priced in the Cloud18 marketplace:\n\n- **csv-service-plan** (default): each cluster is priced from a per-cluster service plan downloaded as CSV.\n- **global-unit-pricing**: all clusters are priced from a single global EUR price per Database Unit and per Application Unit — no per-cluster plan.\n\nThe calculator button recomputes and persists each cluster's Database Units and Application Units immediately, instead of waiting for the next periodic save cycle — useful right after switching mode or changing a cluster's sizing.\n\nConfig: \`cloud18-marketplace-pricing-mode\``
  const hDbuPrice = `**Database Unit Price**\n\nPrice in EUR per Database Unit (1 core / 4GB RAM / 40GB disk / 1000 IOPS).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-dbu-price\``
  const hAppUnitPrice = `**Application Unit Price**\n\nPrice in EUR per Application Unit (application credits used, plus proxy CPU contribution).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-app-unit-price\``
  const hMonthlyInfraCost = `**Monthly Infrastructure Cost**\n\nGlobal monthly infrastructure cost for this marketplace instance, shared by every cluster it hosts.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-monthly-infra-cost\``
  const hMonthlyLicenseCost = `**Monthly License Cost**\n\nGlobal monthly license cost for this marketplace instance.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-monthly-license-cost\``
  const hMonthlySysopsCost = `**Monthly SysOps Cost**\n\nGlobal monthly system operations cost for this marketplace instance.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-monthly-sysops-cost\``
  const hMonthlyDbopsCost = `**Monthly DBOps Cost**\n\nGlobal monthly database operations cost for this marketplace instance.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-monthly-dbops-cost\``
  const hCostCurrency = `**Cost Currency**\n\nCurrency code used for the monthly cost and promotion fields above (e.g. EUR, USD).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-cost-currency\``
  const hPromotionPct = `**Promotion Percentage**\n\nDiscount percentage (0-100) applied to the total monthly cost when advertised in the marketplace.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-promotion-pct\``
  const hInfraCpuModel = `**Infrastructure CPU Model**\n\nCPU model powering this marketplace instance's infrastructure (e.g. AMD EPYC 7763).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-infra-cpu-model\``
  const hInfraCpuFreq = `**Infrastructure CPU Frequency**\n\nCPU clock frequency of this marketplace instance's infrastructure (e.g. 2.45GHz).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-infra-cpu-freq\``
  const hInfraDescription = `**Infrastructure Description**\n\nFree-form description of this marketplace instance's infrastructure (e.g. bare-metal, dedicated hosting, cloud provider).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-infra-description\``
  const hInfraDataCenters = `**Infrastructure Data Centers**\n\nData centers hosting this marketplace instance's infrastructure.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-infra-data-centers\``
  const hInfraPublicBandwidth = `**Infrastructure Public Bandwidth**\n\nPublic network bandwidth (Mbps) available to this marketplace instance's infrastructure.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-infra-public-bandwidth\``
  const hInfraGeoLocalizations = `**Infrastructure Geo Localizations**\n\nGeographic zone(s) of this marketplace instance's infrastructure.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-infra-geo-localizations\``
  const hInfraCertifications = `**Infrastructure Certifications**\n\nCompliance certifications held by this marketplace instance's infrastructure (e.g. ISO 27001, SOC 2).\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-infra-certifications\``
  const hSlaResponseTime = `**SLA Response Time**\n\nGuaranteed incident response time in hours for this marketplace instance.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-sla-response-time\``
  const hSlaRepairTime = `**SLA Repair Time**\n\nGuaranteed incident repair time in hours for this marketplace instance.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-sla-repair-time\``
  const hSlaProvisionTime = `**SLA Provision Time**\n\nGuaranteed provisioning time in hours for this marketplace instance.\nOnly used when pricing mode is **global-unit-pricing**.\n\nConfig: \`cloud18-marketplace-sla-provision-time\``

  // Pricing mode is the control that decides which of the sections below apply,
  // so it stays outside the collapsible cards, always visible at the top.
  const pricingModeRow = [
    {
      key: 'Marketplace Pricing Mode',
      help: h(hPricingMode, 'Marketplace Pricing Mode'),
      value: (
        <Flex align='center' gap={2}>
          <Dropdown
            options={[
              { value: 'csv-service-plan', label: 'CSV Service Plan (per-cluster)' },
              { value: 'global-unit-pricing', label: 'Global Unit Pricing (EUR / DBU + App Unit)' }
            ]}
            selectedValue={pricingMode}
            confirmTitle='Confirm marketplace pricing mode: '
            onChange={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-pricing-mode', value }))}
          />
          <RMIconButton
            icon={HiCalculator}
            tooltip='Recalculate marketplace units now'
            aria-label='Recalculate marketplace units'
            onClick={() => dispatch(recalculateMarketplaceUnits())}
          />
        </Flex>
      )
    }
  ]

  // Unit pricing settings, grouped into cards so the ~17-field unit-pricing
  // form is scannable instead of one long flat list.
  const unitPriceRows = [
    {
      key: 'Database Unit Price (EUR)',
      help: h(hDbuPrice, 'Database Unit Price'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceDbuPrice}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm Database Unit price (EUR) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-dbu-price', value }))}
        />
      )
    },
    {
      key: 'Application Unit Price (EUR)',
      help: h(hAppUnitPrice, 'Application Unit Price'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceAppUnitPrice}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm Application Unit price (EUR) to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-app-unit-price', value }))}
        />
      )
    }
  ]

  const costRows = [
    {
      key: 'Monthly Infrastructure Cost',
      help: h(hMonthlyInfraCost, 'Monthly Infrastructure Cost'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceMonthlyInfraCost}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm monthly infrastructure cost to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-monthly-infra-cost', value }))}
        />
      )
    },
    {
      key: 'Monthly License Cost',
      help: h(hMonthlyLicenseCost, 'Monthly License Cost'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceMonthlyLicenseCost}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm monthly license cost to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-monthly-license-cost', value }))}
        />
      )
    },
    {
      key: 'Monthly SysOps Cost',
      help: h(hMonthlySysopsCost, 'Monthly SysOps Cost'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceMonthlySysopsCost}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm monthly sysops cost to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-monthly-sysops-cost', value }))}
        />
      )
    },
    {
      key: 'Monthly DBOps Cost',
      help: h(hMonthlyDbopsCost, 'Monthly DBOps Cost'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceMonthlyDbopsCost}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm monthly dbops cost to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-monthly-dbops-cost', value }))}
        />
      )
    },
    {
      key: 'Cost Currency',
      help: h(hCostCurrency, 'Cost Currency'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceCostCurrency}
          confirmTitle='Confirm cost currency to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-cost-currency', value }))}
        />
      )
    },
    {
      key: 'Promotion Percentage',
      help: h(hPromotionPct, 'Promotion Percentage'),
      value: (
        <TextForm
          value={config?.cloud18MarketplacePromotionPct}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm promotion percentage to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-promotion-pct', value }))}
        />
      )
    }
  ]

  const infraRows = [
    {
      key: 'Infrastructure CPU Model',
      help: h(hInfraCpuModel, 'Infrastructure CPU Model'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceInfraCpuModel}
          confirmTitle='Confirm infrastructure CPU model to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-infra-cpu-model', value }))}
        />
      )
    },
    {
      key: 'Infrastructure CPU Frequency',
      help: h(hInfraCpuFreq, 'Infrastructure CPU Frequency'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceInfraCpuFreq}
          confirmTitle='Confirm infrastructure CPU frequency to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-infra-cpu-freq', value }))}
        />
      )
    },
    {
      key: 'Infrastructure Description',
      help: h(hInfraDescription, 'Infrastructure Description'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceInfraDescription}
          confirmTitle='Confirm infrastructure description to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-infra-description', value }))}
        />
      )
    },
    {
      key: 'Infrastructure Data Centers',
      help: h(hInfraDataCenters, 'Infrastructure Data Centers'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceInfraDataCenters}
          confirmTitle='Confirm infrastructure data centers to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-infra-data-centers', value }))}
        />
      )
    },
    {
      key: 'Infrastructure Public Bandwidth (Mbps)',
      help: h(hInfraPublicBandwidth, 'Infrastructure Public Bandwidth'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceInfraPublicBandwidth}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm infrastructure public bandwidth to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-infra-public-bandwidth', value }))}
        />
      )
    },
    {
      key: 'Infrastructure Geo Localizations',
      help: h(hInfraGeoLocalizations, 'Infrastructure Geo Localizations'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceInfraGeoLocalizations}
          confirmTitle='Confirm infrastructure geo localizations to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-infra-geo-localizations', value }))}
        />
      )
    },
    {
      key: 'Infrastructure Certifications',
      help: h(hInfraCertifications, 'Infrastructure Certifications'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceInfraCertifications}
          confirmTitle='Confirm infrastructure certifications to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-infra-certifications', value }))}
        />
      )
    }
  ]

  const slaRows = [
    {
      key: 'SLA Response Time (hours)',
      help: h(hSlaResponseTime, 'SLA Response Time'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceSlaResponseTime}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm SLA response time to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-sla-response-time', value }))}
        />
      )
    },
    {
      key: 'SLA Repair Time (hours)',
      help: h(hSlaRepairTime, 'SLA Repair Time'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceSlaRepairTime}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm SLA repair time to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-sla-repair-time', value }))}
        />
      )
    },
    {
      key: 'SLA Provision Time (hours)',
      help: h(hSlaProvisionTime, 'SLA Provision Time'),
      value: (
        <TextForm
          value={config?.cloud18MarketplaceSlaProvisionTime}
          regexPattern='^\d+(\.\d+)?$'
          confirmTitle='Confirm SLA provision time to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-marketplace-sla-provision-time', value }))}
        />
      )
    }
  ]

  const platformGatewayRows = [
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
    }
  ]

  const domainAutomationRows = [
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
    }
  ]

  const plansRows = [
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
    }
  ]

  // Lightweight, non-collapsible group label — reuses the label/sub-label
  // color pairing TableType2 already uses internally (tertiary/quaternary),
  // so a group reads as "nested under Market Place" rather than another
  // top-level accordion section.
  const sectionHeading = (text) => <Box className={styles.subSectionHeading}>{text}</Box>

  const settingsSection = (heading, data) => (
    <>
      {sectionHeading(heading)}
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={data} className={`${styles.tableWithHelp} ${styles.tableFlushTop}`} helpColumn />
      </Flex>
    </>
  )

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={pricingModeRow} className={styles.tableWithHelp} helpColumn />
      </Flex>
      {isUnitPricing && (
        <>
          {settingsSection('Unit Prices', unitPriceRows)}
          {settingsSection('Costs & Currency', costRows)}
          {settingsSection('Infrastructure', infraRows)}
          {settingsSection('SLA', slaRows)}
        </>
      )}
      {settingsSection('Platform & Gateway', platformGatewayRows)}
      {settingsSection('Domain Automation', domainAutomationRows)}
      {!isUnitPricing && settingsSection('Plans', plansRows)}
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
