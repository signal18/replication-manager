import React, { useCallback, useEffect, useReducer, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterPeers, getClusterForSale, getTermsData } from '../../redux/globalClustersSlice'
import { Box, Flex, HStack, Text, Wrap } from '@chakra-ui/react'
import NotFound from '../../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import Card from '../../components/Card'
import TableType2 from '../../components/TableType2'
import styles from './styles.module.scss'
import CustomIcon from '../../components/Icons/CustomIcon'
import TagPill from '../../components/TagPill'
import { HiCreditCard, HiExclamation, HiQuestionMarkCircle, HiTag } from 'react-icons/hi'
import { peerLogin, setBaseURL } from '../../redux/authSlice'
import { getClusterData, clusterSubscribe } from '../../redux/clusterSlice'
import TermsModal from '../../components/Modals/TermsModal'
import { showErrorToast } from '../../redux/toastSlice'
import CheckOrCrossIcon from '../../components/Icons/CheckOrCrossIcon'
import SearchBox from '../../components/SearchBox'
import Dropdown from '../../components/Dropdown'

const defaultFilter = { domain: "", subdomain: "", zone: "", plan: "", search: "", domainOptions: [], subdomainOptions: [], zoneOptions: [], planOptions: [] }

const filterReducer = (state, action) => {
  switch (action.type) {
    case 'domain':
      return { ...state, domain: action.value }
    case 'subdomain':
      return { ...state, subdomain: action.value }
    case 'zone':
      return { ...state, zone: action.value }
    case 'plan':
      return { ...state, plan: action.value }
    case 'search':
      return { ...state, search: action.value }
    case 'domain-options':
      return { ...state, domainOptions: action.value }
    case 'subdomain-options':
      return { ...state, subdomainOptions: action.value }
    case 'zone-options':
      return { ...state, zoneOptions: action.value }
    case 'plan-options':
      return { ...state, planOptions: action.value }
    case 'set':
      return { ...state, ...action.value }
    case 'reset':
      return defaultFilter
    default:
      return state
  }
}

const filterFunc = (cluster, domain, subdomain, zone, plan, search) => {
  let found = true
  if (domain !== "") {
    found = cluster['cloud18-domain'] === domain
  }
  if (subdomain !== "") {
    found = found && cluster['cloud18-sub-domain'] === subdomain
  }
  if (zone !== "") {
    found = found && cluster['cloud18-sub-domain-zone'] === zone
  }
  if (plan !== "") {
    if (plan === "-") {
      found = found && cluster['prov-service-plan'] === ""
    } else {
      found = found && cluster['prov-service-plan'] === plan
    }
  }
  if (search !== "") {
    found = found && cluster['cluster-name'].toLowerCase().includes(search.toLowerCase())
  }
  return found
}

const getOption = (option) => {
  return { name: option || "-", value: option || "-" }
}

const getDomainOptions = (clusterlist) => {
  return [{ name: "Select domain", value: "" }, ...[...new Set(clusterlist?.map(cluster => cluster['cloud18-domain']))].map(option => getOption(option))]
}

const getSubDomainOptions = (clusterlist, domain = "") => {
  return [{ name: "Select subdomain", value: "" }, ...[...new Set(clusterlist?.filter((cluster) => domain === "" || cluster['cloud18-domain'] === domain).map(cluster => cluster['cloud18-sub-domain']))].map(option => getOption(option))]
}

const getZone = (clusterlist, domain = "", subdomain = "") => {
  return [{ name: "Select zone", value: "" }, ...[...new Set(clusterlist?.filter((cluster) => (domain === "" || cluster['cloud18-domain'] === domain) && (subdomain === "" || cluster["cloud18-sub-domain"] === subdomain)).map(cluster => cluster['cloud18-sub-domain-zone']))].map(option => getOption(option))]
}

const getPlanOptions = (clusterlist, domain = "", subdomain = "", zone = "") => {
  return [{ name: "Select plan", value: "" }, ...[...new Set(clusterlist?.filter((cluster) => (domain === "" || cluster['cloud18-domain'] === domain) && (subdomain === "" || cluster["cloud18-sub-domain"] === subdomain) && (zone === "" || cluster["cloud18-sub-domain-zone"] === zone)).map(cluster => cluster['prov-service-plan']))].map(option => getOption(option))]
}

function PeerClusterList({ onLogin, mode }) {
  const dispatch = useDispatch()
  const [clusters, setClusters] = useState([])
  const [filter, fdispatch] = useReducer(filterReducer, defaultFilter)
  const [finalTerms, setFinalTerms] = useState(``)
  const [isTermsModalOpen, setIsTermsModalOpen] = useState(false)

  const { domain, subdomain, zone, plan, search, domainOptions, subdomainOptions, zoneOptions, planOptions } = filter

  const {
    globalClusters: { loading, clusterPeers, clusterForSale, monitor, terms },
    auth: {
      user
    },
  } = useSelector((state) => state)

  useEffect(() => {
    dispatch(getClusterPeers({}))
    dispatch(getClusterForSale({}))
    dispatch(getTermsData({}))
  }, [])

  useEffect(() => {
    if (clusterPeers?.length > 0 && mode !== 'shared') {
      setClusters(clusterPeers?.filter((cluster) => filterFunc(cluster, domain, subdomain, zone, plan, search)) || [])
      fdispatch({ type: "set", value: { domainOptions: getDomainOptions(clusterPeers), subdomainOptions: getSubDomainOptions(clusterPeers, domain), zoneOptions: getZone(clusterPeers, domain, subdomain), planOptions: getPlanOptions(clusterPeers, domain, subdomain, zone) } })
    }
    if (clusterForSale?.length > 0 && mode === 'shared') {
      setClusters(clusterForSale?.filter((cluster) => filterFunc(cluster, domain, subdomain, zone, plan, search)) || [])
      fdispatch({ type: "set", value: { domainOptions: getDomainOptions(clusterForSale), subdomainOptions: getSubDomainOptions(clusterForSale, domain), zoneOptions: getZone(clusterForSale, domain, subdomain), planOptions: getPlanOptions(clusterForSale, domain, subdomain, zone) } })
    }
  }, [clusterPeers, clusterForSale, search, domain, subdomain, zone, plan])

  let header = `
| Label | Value |
| --- | --- |
`

  const parseTerms = useCallback((cluster, newterms = ``) => {
    let servicePlan = Object.entries(cluster)
      .filter(([key]) => !([].includes(key))) // fields to remove
      .map(([key, value]) => `| ${key} | ${value} |`)
      .join("\n");
    let finalterm = newterms
      .replace(`<<user>>`, user?.username)
      .replace(`<<cluster>>`, cluster?.["cluster-name"])
      .replace(`<<ervice_plan_infos>>`, header.concat(servicePlan))
      .replace(`<<date>>`, (new Date()).toLocaleDateString())
    setFinalTerms(finalterm)
    openTermsModal()
  }, [user?.username])

  const openTermsModal = () => {
    setIsTermsModalOpen(true)
  }

  const closeTermsModal = (keepBaseURL = false) => {
    if (!keepBaseURL) {
      dispatch(setBaseURL({ baseURL: '' }))
    }
    setIsTermsModalOpen(false)
  }

  const handleSubscribeModal = (clusterItem) => {
    let baseURL = clusterItem['api-public-url']
    if (monitor?.config?.apiPublicUrl == baseURL) {
      baseURL = ''
    }


    closeTermsModal(true)
    dispatch(clusterSubscribe({ clusterName: clusterItem['cluster-name'], baseURL: baseURL }))
  }

  const handlePeerCluster = (clusterItem, isRelogin = false) => {
    let handler
    let baseURL = clusterItem['api-public-url']
    let token = localStorage.getItem(`user_token`)

    if (monitor?.config?.apiPublicUrl == baseURL) {
      baseURL = ''
    }

    if (baseURL !== '') {
      token = localStorage.getItem(`user_token_${btoa(baseURL)}`)
    }

    if (token && !isRelogin) {
      dispatch(setBaseURL({ baseURL: baseURL }));
      handler = dispatch(getClusterData({ clusterName: clusterItem['cluster-name'] }))
    } else {
      localStorage.removeItem(`user_token_${btoa(baseURL)}`)
      handler = dispatch(peerLogin({ baseURL: baseURL }))
        .then((action) => {
          if (action?.payload?.status === 200) {
            return dispatch(getClusterData({ clusterName: clusterItem['cluster-name'] }))
          } else {
            dispatch(
              showErrorToast({
                status: 'error',
                title: 'Peer login failed',
                description: action?.payload?.data || error
              })
            )
            dispatch(setBaseURL({ baseURL: '' }));
            throw new Error(action?.payload?.data);
          }
        });
    }

    handler.then((resp) => {
      // Handle peer relogin if peer repman instance was restarted
      if (!isRelogin && resp?.payload?.status === 401 && resp?.payload?.data.includes("crypto/rsa: verification error")) {
        return handlePeerCluster(clusterItem, true)
      }

      if (mode === "shared") {
        dispatch(getTermsData({})).then((action) => {
          let newterms = action?.payload?.data || ``
          parseTerms(clusterItem, newterms);
        });
      } else {
        if (resp?.payload?.status === 200) {
          if (onLogin) return onLogin(resp.payload.data);
        }

        dispatch(setBaseURL({ baseURL: '' }));
        dispatch(showErrorToast({
          status: 'error',
          title: 'Peer login failed',
          description: resp?.payload?.data || "Peer login failed"
        }));
      }
    })
  };

  return !loading && clusters?.length === 0 ? (
    <>
      {(search !== "" || plan !== "") && (
        <>
          <Flex className={styles.filterWrapper} gap={4}>
            <Dropdown label={"Domain"} options={domainOptions} className={styles.dropdownButton} selectedValue={domain} onChange={(opt) => fdispatch({ type: 'domain', value: opt.value })} />
            <Dropdown label={"Subdomain"} options={subdomainOptions} className={styles.dropdownButton} selectedValue={subdomain} onChange={(opt) => fdispatch({ type: 'subdomain', value: opt.value })} />
            <Dropdown label={"Zone"} options={zoneOptions} className={styles.dropdownButton} selectedValue={zone} onChange={(opt) => fdispatch({ type: 'zone', value: opt.value })} />
            <Dropdown label={"Plan"} options={planOptions} className={styles.dropdownButton} selectedValue={plan} onChange={(opt) => fdispatch({ type: 'plan', value: opt.value })} />
            <SearchBox className={styles.searchBox} value={search} size='md' placeholder='Search' onChange={(v) => { fdispatch({ type: 'search', value: v }) }} />
          </Flex>
        </>
      )}
      {search === "" && <NotFound text={mode === 'shared' ? 'No shared peer cluster found!' : 'No peer cluster found!'} />}
    </>
  ) : (
    <>
      <Flex className={styles.filterWrapper} gap={4}>
        <Dropdown label={"Domain"} options={domainOptions} className={styles.dropdownButton} selectedValue={domain} onChange={(opt) => fdispatch({ type: 'domain', value: opt.value })} />
        <Dropdown label={"Subdomain"} options={subdomainOptions} className={styles.dropdownButton} selectedValue={subdomain} onChange={(opt) => fdispatch({ type: 'subdomain', value: opt.value })} />
        <Dropdown label={"Zone"} options={zoneOptions} className={styles.dropdownButton} selectedValue={zone} onChange={(opt) => fdispatch({ type: 'zone', value: opt.value })} />
        <Dropdown label={"Plan"} options={planOptions} className={styles.dropdownButton} selectedValue={plan} onChange={(opt) => fdispatch({ type: 'plan', value: opt.value })} />
        <SearchBox className={styles.searchBox} value={search} size='md' placeholder='Search' onChange={(v) => { fdispatch({ type: 'search', value: v }) }} />
      </Flex>
      <Flex className={styles.clusterList}>
        {clusters?.map((clusterItem) => {
          const headerText = `${clusterItem['cluster-name']}\n`
          const domain = `${clusterItem['cloud18-domain']}`
          const subDomain = `${clusterItem['cloud18-sub-domain']}`
          const subDomainZone = ` ${clusterItem['cloud18-sub-domain-zone']}`
          const cost = clusterItem['cloud18-monthly-infra-cost'] * 1 + clusterItem['cloud18-monthly-license-cost'] * 1 + clusterItem['cloud18-monthly-sysops-cost'] * 1 + clusterItem['cloud18-monthly-dbops-cost'] * 1
          const amount = (cost * (100 - clusterItem['cloud18-promotion-pct'])) / 100
          const currency = clusterItem['cloud18-cost-currency']

          const isPending = clusterItem?.['api-credentials-acl-allow']?.includes('pending')
          const isSponsor = clusterItem?.['api-credentials-acl-allow']?.includes('sponsor')

          const dataObject = [
            {
              key: 'Tags', value: (
                <>
                  <Wrap>
                    <TagPill text='cloud18' colorScheme='blue' />
                    <TagPill text={domain} colorScheme='blue' />
                    <TagPill text={subDomain} colorScheme='blue' />
                    <TagPill text={subDomainZone} colorScheme='blue' />
                  </Wrap>
                </>
              )
            },
            {
              key: 'Is Healthy', value: (
                <HStack spacing='4'>
                  {clusterItem?.lastUpdate == "0001-01-01T00:00:00Z" ? (
                    <>
                      <CustomIcon icon={HiQuestionMarkCircle} color='gray' />
                      <Text>Unknown</Text>
                    </>
                  ) : clusterItem?.isDown || clusterItem?.isMasterDown ? (
                    <>
                      <CheckOrCrossIcon isValid={false} />
                      <Text>No</Text>
                    </>
                  ) : !clusterItem?.isFailable ? (
                    <>
                      <CustomIcon icon={HiExclamation} color='orange' />
                      <Text>Warning</Text>
                    </>
                  ) : (
                    <>
                      <CheckOrCrossIcon isValid={true} />
                      <Text>Yes</Text>
                    </>
                  )}
                </HStack>
              )
            },
            {
              key: 'Is Provisioned', value: (<HStack spacing='4'>
                {clusterItem?.lastUpdate == "0001-01-01T00:00:00Z" ? (
                  <>
                    <CustomIcon icon={HiQuestionMarkCircle} color='gray' />
                    <Text>Unknown</Text>
                  </>
                ) : clusterItem?.isProvisioned ? (
                  <>
                    <CheckOrCrossIcon isValid={true} />
                    <Text>Yes</Text>
                  </>
                ) : (
                  <>
                    <CheckOrCrossIcon isValid={false} />
                    <Text>No</Text>
                  </>
                )}
              </HStack>)
            },
            { key: 'Service Plan', value: clusterItem['prov-service-plan'] },
            { key: 'Geo Zone', value: clusterItem['cloud18-infra-geo-localizations'] },
            {
              key: (
                <HStack spacing='4'>
                  {clusterItem['cloud18-promotion-pct'] && clusterItem['cloud18-promotion-pct'] > 0 ? (
                    <>
                      <Text>Price</Text>
                      <CustomIcon color={"red"} icon={HiTag} />
                    </>
                  ) : (
                    <>
                      <Text>Price</Text>
                    </>
                  )}
                </HStack>
              ), value: (
                <HStack spacing='4'>
                  {clusterItem['cloud18-promotion-pct'] && clusterItem['cloud18-promotion-pct'] > 0 ? (
                    <>
                      <Text>
                        <Text as={"span"} textColor="red.500" textDecorationColor="red.500" textDecoration="line-through">
                          {cost.toFixed(2)}
                        </Text>
                        &nbsp;
                        <Text as={"span"} fontWeight="bold">
                          {amount.toFixed(2)} {currency}/Month
                        </Text>
                      </Text>
                    </>
                  ) : (
                    <>
                      <Text>{cost.toFixed(2)} {currency}/Month</Text>
                    </>
                  )}
                </HStack>
              )
            },
            { key: 'Memory', value: clusterItem['prov-db-memory'] / 1024 + "GB" },
            { key: 'IOps', value: clusterItem['prov-db-disk-iops'] },
            { key: 'Disk', value: clusterItem['prov-db-disk-size'] + "GB" },
            { key: 'CPU Core', value: clusterItem['prov-db-cpu-cores'] },
            { key: 'CPU Type', value: clusterItem['cloud18-infra-cpu-model'] },
            { key: 'CPU Freq', value: clusterItem['cloud18-infra-cpu-freq'] },
            { key: 'Data Centers', value: clusterItem['cloud18-infra-data-centers'] },
            { key: 'Public Bandwidth', value: clusterItem['cloud18-infra-public-bandwidth'] / 1024 + "Gbps" },
            { key: 'Time To Response', value: clusterItem['cloud18-sla-response-time'] + "Hours" },
            { key: 'Time To Repair', value: clusterItem['cloud18-sla-repair-time'] + "Hours" },
            { key: 'Time To Provision', value: clusterItem['cloud18-sla-provision-time'] + "Hours" },
            { key: 'Certifications', value: clusterItem['cloud18-infra-certifications'] },
            { key: 'Infrastructure', value: clusterItem['prov-orchestrator'] + " " + clusterItem['cloud18-platform-description'] },
            /*  {
                key: 'Share',
                value: (
                  <HStack spacing='4'>
                    {clusterItem['cloud18-is-multi-dc'] ? (
                      <>
                        <CheckOrCrossIcon isValid={true} />
                        <Text>Yes</Text>
                      </>
                    ) : (
                      <>
                        <CheckOrCrossIcon isValid={false} />
                        <Text>No</Text>
                      </>
                    )}
                  </HStack>
                )
              }*/
          ]

          return (
            <Box key={clusterItem['cluster-name']} className={styles.cardWrapper}>
              <Card
                className={styles.card}
                width={'400px'}
                header={
                  <HStack
                    as="button"
                    className={styles.btnHeading}
                    onClick={() => { handlePeerCluster(clusterItem) }}>
                    <CustomIcon icon={isSponsor || isPending ? (HiCreditCard) : (AiOutlineCluster)} fill={isSponsor ? "green" : isPending ? "orange" : "gray"} />
                    <span className={styles.cardHeaderText}>{headerText}</span>
                  </HStack>
                }
                body={
                  <TableType2
                    dataArray={dataObject}
                    className={styles.table}
                    labelClassName={styles.rowLabel}
                    valueClassName={styles.rowValue}
                  />
                }
              />
            </Box>
          )
        })}
      </Flex>
      {isTermsModalOpen && <TermsModal terms={finalTerms} isOpen={isTermsModalOpen} closeModal={closeTermsModal} onSaveModal={handleSubscribeModal} />}
    </>
  )
}

export default PeerClusterList
