import React, { useCallback, useEffect, useMemo, useReducer, useState, useRef } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterPeers, getClusterForSale, getClusterPeerNodes, getTermsData } from '../../redux/globalClustersSlice'
import { Badge, Box, Flex, HStack, Link, Text, Wrap, IconButton, useColorModeValue, Collapse, Divider, Tooltip, ButtonGroup, Button } from '@chakra-ui/react'
import { FaCloud } from 'react-icons/fa'
import NotFound from '../../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import Card from '../../components/Card'
import TableType2 from '../../components/TableType2'
import { DataTable } from '../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import styles from './styles.module.scss'
import CustomIcon from '../../components/Icons/CustomIcon'
import TagPill from '../../components/TagPill'
import { HiCreditCard, HiExclamation, HiQuestionMarkCircle, HiTag, HiSortAscending, HiSortDescending, HiSearch, HiChevronLeft, HiChevronRight } from 'react-icons/hi'
import { peerLogin, setBaseURL } from '../../redux/authSlice'
import { getClusterData, clusterSubscribe } from '../../redux/clusterSlice'
import TermsModal from '../../components/Modals/TermsModal'
import { showErrorToast } from '../../redux/toastSlice'
import CheckOrCrossIcon from '../../components/Icons/CheckOrCrossIcon'
import SearchBox from '../../components/SearchBox'
import Dropdown from '../../components/Dropdown'

const columnHelper = createColumnHelper()

const defaultFilter = {
  domain: "",
  subdomain: "",
  zone: "",
  plan: "",
  search: "",
  domainOptions: [],
  subdomainOptions: [],
  zoneOptions: [],
  planOptions: [],
  sortBy: "price", // Default sort by price
  sortOrder: "asc"
}

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
    case 'sort':
      return {
        ...state,
        sortBy: action.value,
        // If clicking the same sort option, toggle the order
        sortOrder: state.sortBy === action.value ? (state.sortOrder === 'asc' ? 'desc' : 'asc') : 'asc'
      }
    case 'sort-order':
      return { ...state, sortOrder: action.value }
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

// Array of sort options for dropdown
const sortOptions = [
  { name: "Default", value: "" },
  { name: "Price", value: "price" },
  { name: "CPU Cores", value: "cpu" },
  { name: "Memory", value: "memory" }
]

function PeerClusterList({ onLogin, mode }) {
  const dispatch = useDispatch()
  const [clusters, setClusters] = useState([])
  const [filter, fdispatch] = useReducer(filterReducer, defaultFilter)
  const [finalTerms, setFinalTerms] = useState(``)
  const [isTermsModalOpen, setIsTermsModalOpen] = useState(false)
  const [item, setItem] = useState({})
  // Two table views, one visible via toggle: 'operational' (local-like) and
  // 'offer' (infra + commercial — what you get for the price). URI + Active columns
  // appear in both as the transparency anchor.
  const [viewType, setViewType] = useState('operational')
  // Keep the search/filter panel retracted by default so the URI/Active columns
  // lead; the operator can expand it manually.
  const [isFilterPanelOpen, setIsFilterPanelOpen] = useState(false)
  const mainContentRef = useRef(null)

  // Auto-close filter panel when mouse moves to cluster list
  const handleMouseEnterClusterList = () => {
    if (window.innerWidth < 1200 && isFilterPanelOpen) {
      setIsFilterPanelOpen(false)
    }
  }

  const { domain, subdomain, zone, plan, search, domainOptions, subdomainOptions, zoneOptions, planOptions, sortBy, sortOrder } = filter

  const {
    globalClusters: { loading, clusterPeers, clusterForSale, peerNodes, monitor, terms },
    auth: {
      user
    },
  } = useSelector((state) => state)

  useEffect(() => {
    dispatch(getClusterPeers({}))
    dispatch(getClusterForSale({}))
    dispatch(getClusterPeerNodes({}))
    dispatch(getTermsData({}))
  }, [])

  useEffect(() => {
    let filteredClusters = []

    if (clusterPeers?.length > 0 && mode !== 'shared') {
      filteredClusters = clusterPeers?.filter((cluster) => filterFunc(cluster, domain, subdomain, zone, plan, search)) || []
      fdispatch({ type: "set", value: { domainOptions: getDomainOptions(clusterPeers), subdomainOptions: getSubDomainOptions(clusterPeers, domain), zoneOptions: getZone(clusterPeers, domain, subdomain), planOptions: getPlanOptions(clusterPeers, domain, subdomain, zone) } })
    }

    if (clusterForSale?.length > 0 && mode === 'shared') {
      filteredClusters = clusterForSale?.filter((cluster) => filterFunc(cluster, domain, subdomain, zone, plan, search)) || []
      fdispatch({ type: "set", value: { domainOptions: getDomainOptions(clusterForSale), subdomainOptions: getSubDomainOptions(clusterForSale, domain), zoneOptions: getZone(clusterForSale, domain, subdomain), planOptions: getPlanOptions(clusterForSale, domain, subdomain, zone) } })
    }

    // Apply sorting if selected
    if (sortBy) {
      filteredClusters = sortClusters(filteredClusters, sortBy, sortOrder)
    }

    setClusters(filteredClusters)
  }, [clusterPeers, clusterForSale, search, domain, subdomain, zone, plan, sortBy, sortOrder])

  // Function to sort clusters based on selected criteria
  const sortClusters = (clustersList, sortCriteria, order) => {
    return [...clustersList].sort((a, b) => {
      let comparison = 0

      switch (sortCriteria) {
        case 'price':
          // Calculate total cost for each cluster
          const costA = a['cloud18-monthly-infra-cost'] * 1 + a['cloud18-monthly-license-cost'] * 1 +
                        a['cloud18-monthly-sysops-cost'] * 1 + a['cloud18-monthly-dbops-cost'] * 1
          const costB = b['cloud18-monthly-infra-cost'] * 1 + b['cloud18-monthly-license-cost'] * 1 +
                        b['cloud18-monthly-sysops-cost'] * 1 + b['cloud18-monthly-dbops-cost'] * 1

          // Apply promotion if any
          const amountA = (costA * (100 - a['cloud18-promotion-pct'])) / 100
          const amountB = (costB * (100 - b['cloud18-promotion-pct'])) / 100

          comparison = amountA - amountB
          break

        case 'cpu':
          // Sort by CPU cores
          comparison = parseFloat(a['prov-db-cpu-cores']) - parseFloat(b['prov-db-cpu-cores'])
          break

        case 'memory':
          // Sort by memory (converted to GB for consistency)
          comparison = parseFloat(a['prov-db-memory']) - parseFloat(b['prov-db-memory'])
          break

        default:
          // Default sort by cluster name
          comparison = a['cluster-name'].localeCompare(b['cluster-name'])
      }

      // Apply sort order
      return order === 'asc' ? comparison : -comparison
    })
  }

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
    setItem(cluster)
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

  // Status icon for the card header. Health-first: a cluster is green when it's up
  // and failable, regardless of isProvisioned (the BO peer.json often reports
  // isProvisioned=false for shared clusters even when they're running). Only
  // "unknown" when the peer carries no health fields at all. Mirrors healthCell.
  const getStatusIcon = (clusterItem) => {
    if (isUnknown(clusterItem)) {
      return { icon: HiQuestionMarkCircle, color: 'gray.400', tooltip: 'Unknown — no health reported by peer' }
    }
    if (clusterItem?.isDown || clusterItem?.isMasterDown) {
      return { icon: 'circle', color: 'red.500', tooltip: clusterItem?.isMasterDown ? 'Master down' : 'Down' }
    }
    if (!clusterItem?.isFailable) {
      return { icon: 'circle', color: 'orange.500', tooltip: 'Not failable (blocker)' }
    }
    return {
      icon: 'circle',
      color: 'green.500',
      tooltip: clusterItem?.isProvisioned ? 'Healthy & provisioned' : 'Healthy (provisioning not reported by peer)'
    }
  }

  // Get the current sort status text for display
  const getSortStatusText = () => {
    if (!sortBy) return "";

    let sortField = "";
    switch(sortBy) {
      case 'price': sortField = "Price"; break;
      case 'cpu': sortField = "CPU Cores"; break;
      case 'memory': sortField = "Memory"; break;
      default: return "";
    }

    return `Sorted by ${sortField} (${sortOrder === 'asc' ? 'ascending' : 'descending'})`;
  }

  // Custom circle icon component
  const CircleIcon = ({ color }) => (
    <Box
      w="12px"
      h="12px"
      borderRadius="50%"
      bg={color}
      display="inline-block"
    />
  )

  // getConnState reflects the GROUND TRUTH of whether this instance has actually
  // opened an HTTPS connection to a peer URI, from /api/peers (PeerNodeStatus:
  // LastUpdate is advanced only when we really poll). A for-sale catalog entry we
  // never call has a zero LastUpdate -> "None": a beta tester can match their
  // firewall log against the "Active" rows and confirm the "None" rows generate no
  // outbound traffic. See doc/implementation/peer/MARKETPLACE.md.
  const getConnState = (uri) => {
    const node = peerNodes?.[uri]
    const last = node?.LastUpdate
    const never = !last || last === '0001-01-01T00:00:00Z'
    if (never) {
      // No direct connection -> the health shown comes from peer.json (the BO catalog).
      return { label: 'peer.json', colorScheme: 'gray', tooltip: 'No direct connection — health comes from peer.json (the BO catalog: async, git-pull cadence). No HTTPS call is made from this instance.' }
    }
    const lastDate = new Date(last)
    const lastStr = lastDate.toLocaleString()
    if (node?.Error) {
      // We can't reach the peer directly (firewall / IPv4-vs-IPv6 / NAT / network) —
      // this is NOT a cluster failure. Health falls back to the peer.json catalog
      // value (the BO can reach peers this instance can't). Orange, not red.
      return { label: 'Unreachable', colorScheme: 'orange', tooltip: `This instance can't reach the peer directly (firewall / IPv4-IPv6 / network) — showing the peer.json catalog value. Last try ${lastStr}: ${node.Error}` }
    }
    // Health dispatch cadence is ~2 min; treat a connection within 5 min as fresh.
    const isRecent = Date.now() - lastDate.getTime() < 5 * 60 * 1000
    return isRecent
      ? { label: 'Live', colorScheme: 'green', tooltip: `Direct connection — health is live-verified (last ${lastStr})` }
      : { label: 'Stale', colorScheme: 'orange', tooltip: `Direct connection went stale — last ${lastStr}` }
  }

  // Health is "unknown" only when the peer carries NO health fields at all (e.g. an
  // out-of-cloud18 shared cluster with no metadata). Do NOT gate on lastUpdate: the
  // BO peer.json reports isDown/isMasterDown/isFailable/isProvisioned but NOT
  // lastUpdate, so gating on it wrongly blanks every peer to "—". lastUpdate is only
  // set by a direct poll; the live "Active" column uses /api/peers for that instead.
  const isUnknown = (row) =>
    row?.isFailable === undefined && row?.isDown === undefined &&
    row?.isMasterDown === undefined && row?.isProvisioned === undefined

  const healthCell = (row) => {
    if (isUnknown(row)) return <Text>—</Text>
    if (row.isDown || row.isMasterDown) {
      return <HStack spacing='2'><CheckOrCrossIcon isValid={false} /><Text>No</Text></HStack>
    }
    if (!row.isFailable) {
      return <HStack spacing='2'><CustomIcon icon={HiExclamation} color='red' /><Text>Blocker</Text></HStack>
    }
    return <HStack spacing='2'><CheckOrCrossIcon isValid={true} /><Text>Yes</Text></HStack>
  }

  // Local-cluster-parity cells; "—" until the BO adds these to peer.json.
  const arbCell = (row) => {
    if (row.activePassiveStatus === 'A') return <TagPill colorScheme='green' text='Active' />
    if (row.activePassiveStatus === 'S') return <TagPill colorScheme='orange' text='Standby' />
    return <Text>—</Text>
  }
  const monCell = (row) => (row.monitoringPause === undefined ? <Text>—</Text> : <CheckOrCrossIcon isValid={!row.monitoringPause} />)
  const countOf = (v) => (Array.isArray(v) ? v.length : v)

  const num = (v) => (v === undefined || v === null || v === '' ? '—' : v)

  const priceCell = (row) => {
    const cost = (row['cloud18-monthly-infra-cost'] * 1) + (row['cloud18-monthly-license-cost'] * 1) + (row['cloud18-monthly-sysops-cost'] * 1) + (row['cloud18-monthly-dbops-cost'] * 1)
    if (!cost) return <Text>—</Text>
    const promo = row['cloud18-promotion-pct'] * 1
    const currency = row['cloud18-cost-currency'] || ''
    const amount = promo > 0 ? (cost * (100 - promo)) / 100 : cost
    if (promo > 0) {
      return (
        <HStack spacing='1'>
          <Text as='span' textColor='red.500' textDecoration='line-through'>{cost.toFixed(2)}</Text>
          <Text as='span' fontWeight='bold'>{amount.toFixed(2)} {currency}/mo</Text>
        </HStack>
      )
    }
    return <Text>{cost.toFixed(2)} {currency}/mo</Text>
  }

  // Columns common to both views — the transparency anchor (name + URI + live
  // connection state) is present whatever metadata a peer carries.
  const clusterCol = columnHelper.accessor((row) => row['cluster-name'], {
    id: 'cluster', header: 'Cluster',
    cell: (info) => {
      const row = info.row.original
      const isPending = row?.['api-credentials-acl-allow']?.includes('pending')
      const isSponsor = row?.['api-credentials-acl-allow']?.includes('sponsor')
      return (
        <HStack cursor='pointer' onClick={() => handlePeerCluster(row)}>
          <CustomIcon icon={isSponsor || isPending ? HiCreditCard : AiOutlineCluster} fill={isSponsor ? 'green' : isPending ? 'orange' : 'gray'} />
          <Text fontWeight='semibold'>{info.getValue()}</Text>
        </HStack>
      )
    }
  })
  // Connectivity cloud: green = we have a working direct connection to the peer,
  // red = none / can't reach it (health then comes from the peer.json catalog).
  const connCloud = (uri) => {
    const node = peerNodes?.[uri]
    const last = node?.LastUpdate
    const ok = last && last !== '0001-01-01T00:00:00Z' && !node?.Error
    return ok
      ? { color: 'green', tooltip: `Direct connection OK — last ${new Date(last).toLocaleString()}` }
      : { color: 'red', tooltip: node?.Error ? `Cannot reach peer directly: ${node.Error}` : 'No direct connection — health comes from the peer.json catalog' }
  }
  const uriCol = columnHelper.accessor((row) => row['api-public-url'], {
    id: 'uri', header: 'Peer', enableSorting: false,
    cell: (info) => {
      const uri = info.getValue()
      if (!uri) return <Text>—</Text>
      const host = uri.replace(/^https?:\/\//, '').replace(/\/+$/, '')
      const cc = connCloud(uri)
      return (
        <HStack spacing='2'>
          <Tooltip label={cc.tooltip} placement='top'><Box as='span' display='inline-flex'><CustomIcon icon={FaCloud} color={cc.color} /></Box></Tooltip>
          <Link href={uri} isExternal fontFamily='mono' fontSize='sm'>{host}</Link>
        </HStack>
      )
    }
  })
  // Effective health + its source (the "peer.json default, overwritten by a direct
  // connection" model). healthyCol shows the effective value; directCol shows whether
  // that value is Live (direct-verified) or Catalog (peer.json default).
  const healthyCol = columnHelper.accessor((row) => row, {
    id: 'healthy', header: 'Healthy', enableSorting: false,
    cell: (info) => (
      <Tooltip label='Effective health: peer.json catalog default, overwritten by a direct connection for reachable fleet clusters (the cloud icon on the Peer column shows whether a direct connection is up).' placement='top'>
        <Box display='inline-block'>{healthCell(info.row.original)}</Box>
      </Tooltip>
    )
  })
  // No action column — clicking the cluster name enters/subscribes (the Enter button
  // was redundant).

  // Operational view — same columns and SAME ORDER as the local cluster table
  // (ClusterList), so the two views are consistent. Fields the BO peer.json doesn't
  // carry yet render "—" and fill in once enriched. The Peer column (last) shows the
  // hostname as a link + a green/red cloud for direct connectivity.
  const operationalColumns = useMemo(() => [
    clusterCol,
    columnHelper.accessor((row) => row.activePassiveStatus, { id: 'arbitration', header: 'Arbitration', enableSorting: false, cell: (info) => arbCell(info.row.original) }),
    columnHelper.accessor((row) => row.monitoringPause, { id: 'monitoring', header: 'Monitoring', enableSorting: false, cell: (info) => monCell(info.row.original) }),
    columnHelper.accessor((row) => row.topology, { id: 'topology', header: 'Topology', cell: (info) => info.getValue() || '—' }),
    columnHelper.accessor((row) => row['prov-orchestrator'], { id: 'orchestrator', header: 'Orchestrator', cell: (info) => info.getValue() || '—' }),
    columnHelper.accessor((row) => countOf(row.dbServers), { id: 'databases', header: 'Databases', cell: (info) => num(info.getValue()) }),
    columnHelper.accessor((row) => countOf(row.proxyServers), { id: 'proxies', header: 'Proxies', cell: (info) => num(info.getValue()) }),
    healthyCol,
    columnHelper.accessor((row) => row, { id: 'provisioned', header: 'Provisioned', enableSorting: false, cell: (info) => isUnknown(info.row.original) ? <Text>—</Text> : <CheckOrCrossIcon isValid={!!info.row.original.isProvisioned} /> }),
    columnHelper.accessor((row) => row.uptime, { id: 'sla', header: 'SLA', cell: (info) => info.getValue() || '—' }),
    uriCol,
  ], [peerNodes, mode])

  // Compact infra: a short inline summary, full spec sheet on hover — keeps the
  // Offer table narrow instead of a dozen spec columns.
  const infraSummary = (row) => {
    const parts = []
    if (row['prov-db-cpu-cores']) parts.push(`${row['prov-db-cpu-cores']} core${row['prov-db-cpu-cores'] * 1 === 1 ? '' : 's'}`)
    if (row['prov-db-memory']) parts.push(`${row['prov-db-memory'] / 1024}GB`)
    if (row['prov-db-disk-size']) parts.push(`${row['prov-db-disk-size']}GB disk`)
    if (row['cloud18-infra-cpu-model']) parts.push(row['cloud18-infra-cpu-model'])
    return parts
  }
  const infraDetail = (row) => (
    [
      ['CPU cores', row['prov-db-cpu-cores']],
      ['Memory', row['prov-db-memory'] ? `${row['prov-db-memory'] / 1024}GB` : ''],
      ['Disk', row['prov-db-disk-size'] ? `${row['prov-db-disk-size']}GB` : ''],
      ['Disk IOPS', row['prov-db-disk-iops']],
      ['CPU model', row['cloud18-infra-cpu-model']],
      ['CPU freq', row['cloud18-infra-cpu-freq']],
      ['Data centers', row['cloud18-infra-data-centers']],
      ['Geo', row['cloud18-infra-geo-localizations']],
      ['Bandwidth', row['cloud18-infra-public-bandwidth'] ? `${row['cloud18-infra-public-bandwidth'] / 1024}Gbps` : ''],
      ['Certifications', row['cloud18-infra-certifications']],
    ].filter(([, v]) => v !== undefined && v !== '' && v !== null).map(([k, v]) => `${k}: ${v}`).join('\n')
  )

  // Offer view — infra + commercial together: what you get for the price.
  const offerColumns = useMemo(() => [
    clusterCol, healthyCol,
    columnHelper.accessor((row) => row['prov-service-plan'], { id: 'plan', header: 'Plan', cell: (info) => info.getValue() || '—' }),
    columnHelper.accessor((row) => row, { id: 'price', header: 'Price', enableSorting: false, cell: (info) => priceCell(info.row.original) }),
    columnHelper.accessor((row) => infraSummary(row).join(' · '), {
      id: 'infra', header: 'Infra',
      cell: (info) => {
        const row = info.row.original
        const parts = infraSummary(row)
        if (parts.length === 0) return <Text>—</Text>
        return (<Tooltip label={infraDetail(row)} placement='top' whiteSpace='pre-line'><Text fontSize='sm'>{parts.join(' · ')}</Text></Tooltip>)
      }
    }),
    uriCol,
  ], [peerNodes, mode])

  // Colors for the filter panel based on color mode
  const filterPanelBg = useColorModeValue('gray.50', 'gray.800')
  const filterPanelBorder = useColorModeValue('gray.200', 'gray.700')
  const toggleButtonBg = useColorModeValue('white', 'gray.700')
  const toggleButtonHoverBg = useColorModeValue('gray.100', 'gray.600')

  return (
    <Flex className={styles.contentWrapper}>
      {/* Filter Panel */}
      <Box
        className={styles.filterPanel}
        borderRight={`1px solid`}
        borderColor={filterPanelBorder}
        w={isFilterPanelOpen ? '300px' : '60px'}
        position="sticky"
        top="0"
        pt={4}
        transition="width 0.3s ease"
        height="100%"
      >
        {/* Always visible search/toggle button */}
        <Flex justifyContent="center" alignItems="center" mb={4} px={2}>
          <IconButton
            aria-label={isFilterPanelOpen ? "Collapse filter panel" : "Expand filter panel"}
            icon={isFilterPanelOpen ? <HiChevronLeft /> : <HiSearch />}
            size="sm"
            onClick={() => setIsFilterPanelOpen(!isFilterPanelOpen)}
            bg={toggleButtonBg}
            _hover={{ bg: toggleButtonHoverBg }}
            borderRadius="md"
            boxShadow="sm"
          />
        </Flex>

        <Collapse in={isFilterPanelOpen} animateOpacity>
          <Box px={4}>
            <Text fontWeight="bold" mb={4} textAlign="center">
              Filters
            </Text>

            <SearchBox
              className={styles.searchBox}
              value={search}
              size='md'
              placeholder='Search clusters...'
              onChange={(v) => { fdispatch({ type: 'search', value: v }) }}
              rightIcon={<HiSearch />}
            />

            <Divider my={4} />

            <Text fontWeight="semibold" mb={2}>Domain</Text>
            <Dropdown
              options={domainOptions}
              className={styles.dropdownButton}
              selectedValue={domain}
              onChange={(opt) => fdispatch({ type: 'domain', value: opt.value })}
            />

            <Text fontWeight="semibold" mb={2} mt={4}>Subdomain</Text>
            <Dropdown
              options={subdomainOptions}
              className={styles.dropdownButton}
              selectedValue={subdomain}
              onChange={(opt) => fdispatch({ type: 'subdomain', value: opt.value })}
            />

            <Text fontWeight="semibold" mb={2} mt={4}>Zone</Text>
            <Dropdown
              options={zoneOptions}
              className={styles.dropdownButton}
              selectedValue={zone}
              onChange={(opt) => fdispatch({ type: 'zone', value: opt.value })}
            />

            <Text fontWeight="semibold" mb={2} mt={4}>Plan</Text>
            <Dropdown
              options={planOptions}
              className={styles.dropdownButton}
              selectedValue={plan}
              onChange={(opt) => fdispatch({ type: 'plan', value: opt.value })}
            />

            <Divider my={4} />

            <Text fontWeight="semibold" mb={2}>Sort By</Text>
            <Dropdown
              options={sortOptions}
              className={styles.dropdownButton}
              selectedValue={sortBy}
              onChange={(opt) => fdispatch({ type: 'sort', value: opt.value })}
            />

            {/* Sort Order Buttons */}
            {sortBy && (
              <Box mt={3}>
                <Text fontWeight="semibold" mb={2} fontSize="sm">Sort Order</Text>
                <ButtonGroup size="sm" isAttached width="100%">
                  <Button
                    flex="1"
                    variant={sortOrder === 'asc' ? 'solid' : 'outline'}
                    colorScheme={sortOrder === 'asc' ? 'blue' : 'gray'}
                    leftIcon={<CustomIcon icon={HiSortAscending} />}
                    onClick={() => fdispatch({ type: 'sort-order', value: 'asc' })}
                  >
                    ASC
                  </Button>
                  <Button
                    flex="1"
                    variant={sortOrder === 'desc' ? 'solid' : 'outline'}
                    colorScheme={sortOrder === 'desc' ? 'blue' : 'gray'}
                    leftIcon={<CustomIcon icon={HiSortDescending} />}
                    onClick={() => fdispatch({ type: 'sort-order', value: 'desc' })}
                  >
                    DESC
                  </Button>
                </ButtonGroup>
              </Box>
            )}

            {sortBy && (
              <Flex alignItems="center" mt={2} color="gray.500" fontSize="sm">
                <CustomIcon icon={sortOrder === 'asc' ? HiSortAscending : HiSortDescending} mr={1} />
                <Text>{getSortStatusText()}</Text>
              </Flex>
            )}
          </Box>
        </Collapse>
      </Box>

      {/* Main Content */}
      <Box
        flex="1"
        p={4}
        ref={mainContentRef}
        className={styles.mainContent}
        onMouseEnter={handleMouseEnterClusterList}
      >
        {!loading && clusters?.length === 0 ? (
          <NotFound text={mode === 'shared' ? 'No shared peer cluster found!' : 'No peer cluster found!'} />
        ) : (
          <>
            <Flex justifyContent="space-between" alignItems="center" mb={4}>
              <Text fontSize="lg" fontWeight="bold">
                Showing {clusters.length} cluster{clusters.length !== 1 ? 's' : ''}
              </Text>

              <HStack spacing="2">
                <ButtonGroup size='sm' isAttached variant='outline'>
                  <Button variant={viewType === 'operational' ? 'solid' : 'outline'} colorScheme={viewType === 'operational' ? 'blue' : 'gray'} onClick={() => setViewType('operational')}>Operational</Button>
                  <Button variant={viewType === 'offer' ? 'solid' : 'outline'} colorScheme={viewType === 'offer' ? 'blue' : 'gray'} onClick={() => setViewType('offer')}>Offer</Button>
                  <Button variant={viewType === 'cards' ? 'solid' : 'outline'} colorScheme={viewType === 'cards' ? 'blue' : 'gray'} onClick={() => setViewType('cards')}>Cards</Button>
                </ButtonGroup>
                {/* Only show this on mobile views when panel is collapsed */}
                {!isFilterPanelOpen && (
                  <IconButton
                    display={{ base: 'flex', lg: 'none' }}
                    aria-label="Open filters"
                    icon={<HiSearch />}
                    onClick={() => setIsFilterPanelOpen(true)}
                  />
                )}
              </HStack>
            </Flex>

            {viewType === 'cards' ? (
            <Flex className={styles.clusterList}>
              {clusters?.map((clusterItem) => {
                const headerText = `${clusterItem['cluster-name']}`
                const domain = `${clusterItem['cloud18-domain']}`
                const subDomain = `${clusterItem['cloud18-sub-domain']}`
                const subDomainZone = ` ${clusterItem['cloud18-sub-domain-zone']}`
                const cost = clusterItem['cloud18-monthly-infra-cost'] * 1 + clusterItem['cloud18-monthly-license-cost'] * 1 + clusterItem['cloud18-monthly-sysops-cost'] * 1 + clusterItem['cloud18-monthly-dbops-cost'] * 1
                const amount = (cost * (100 - clusterItem['cloud18-promotion-pct'])) / 100
                const currency = clusterItem['cloud18-cost-currency']

                const isPending = clusterItem?.['api-credentials-acl-allow']?.includes('pending')
                const isSponsor = clusterItem?.['api-credentials-acl-allow']?.includes('sponsor')

                // Get status info
                const statusInfo = getStatusIcon(clusterItem)

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
                ]

                return (
                  <Box key={clusterItem['cluster-name']} className={styles.cardWrapper}>
                    <Card
                      className={styles.card}
                      width={'400px'}
                      header={
                        <HStack as="div" className={styles.btnHeading} cursor={'pointer'} onClick={() => { handlePeerCluster(clusterItem) }} justifyContent="space-between">
                          <HStack>
                            <CustomIcon icon={isSponsor || isPending ? (HiCreditCard) : (AiOutlineCluster)} fill={isSponsor ? "green" : isPending ? "orange" : "gray"} />
                            <span className={styles.cardHeaderText}>{headerText}</span>
                          </HStack>
                          <Tooltip label={statusInfo.tooltip} placement="top">
                            <Box>
                              {statusInfo.icon === HiQuestionMarkCircle ? (
                                <CustomIcon icon={HiQuestionMarkCircle} color={statusInfo.color} />
                              ) : (
                                <CircleIcon color={statusInfo.color} />
                              )}
                            </Box>
                          </Tooltip>
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
            ) : (
              <DataTable data={clusters} columns={viewType === 'offer' ? offerColumns : operationalColumns} />
            )}
          </>
        )}
      </Box>
      {isTermsModalOpen && <TermsModal cluster={item} terms={finalTerms} isOpen={isTermsModalOpen} closeModal={closeTermsModal} onAgreeTerms={handleSubscribeModal} />}
    </Flex>
  )
}

export default PeerClusterList
