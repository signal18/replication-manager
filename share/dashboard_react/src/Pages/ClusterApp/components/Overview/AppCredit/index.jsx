import { useMemo } from 'react'
import styles from '../styles.module.scss'
import { Text, VStack } from '@chakra-ui/react'
import TableType2 from '../../../../../components/TableType2'
import PropTypes from 'prop-types'

function AppCredit({ config, appConfig }) {
    const hasCreditsSet = config?.cloud18ApplicationCredits != null
    const clusterCredits = config?.cloud18ApplicationCredits ?? 0
    const clusterCreditsUsed = config?.cloud18ApplicationCreditsUsed ?? 0
    const clusterCreditsPlanned = config?.cloud18ApplicationCreditsPlanned ?? 0
    const clusterCreditsUnallocated = hasCreditsSet ? clusterCredits - clusterCreditsPlanned : 0
    const provAppCreditPlanned = appConfig?.provAppCreditPlanned || 0
    const provAppAgents = appConfig?.provAppAgents || ''
    const appSizingMode = appConfig?.provAppSizingMode ?? ''
    const clusterSizingMode = config?.provAppSizingMode || ''
    const isUnitMode = (appSizingMode || clusterSizingMode) === 'unit'

    const agentCount = useMemo(() => {
        if (!provAppAgents) return 1
        const list = typeof provAppAgents === 'string'
            ? provAppAgents.split(',').filter(a => a.trim())
            : (Array.isArray(provAppAgents) ? provAppAgents.filter(Boolean) : [])
        return list.length || 1
    }, [provAppAgents])

    const creditIsValid = isUnitMode && agentCount > 0 && provAppCreditPlanned > 0
        ? provAppCreditPlanned % agentCount === 0
        : true
    const appUnitPerAgent = creditIsValid && agentCount > 0 && provAppCreditPlanned > 0
        ? provAppCreditPlanned / agentCount
        : 0

    const dataObject = useMemo(() => {
        const rows = [
            {
                key: "Cloud18 Credit Usage",
                value: hasCreditsSet
                    ? (<Text>{clusterCreditsUsed} / {clusterCredits}</Text>)
                    : (<Text>{'Not set'}</Text>),
            },
            {
                key: "Unallocated Credits",
                value: hasCreditsSet
                    ? (<Text>{clusterCreditsUnallocated} / {clusterCredits}</Text>)
                    : (<Text>{'Not set'}</Text>),
            },
        ]

        if (isUnitMode && provAppCreditPlanned > 0) {
            rows.push({
                key: 'App Units',
                value: creditIsValid ? (
                    <Text>
                        {appUnitPerAgent} App Unit/node × {agentCount} node{agentCount !== 1 ? 's' : ''} = {provAppCreditPlanned} total credits
                    </Text>
                ) : (
                    <Text color='red.500'>
                        Invalid: {provAppCreditPlanned} credit{provAppCreditPlanned !== 1 ? 's' : ''} / {agentCount} agent{agentCount !== 1 ? 's' : ''} — not evenly divisible
                    </Text>
                ),
            })
        }

        return rows
    }, [hasCreditsSet, clusterCreditsUsed, clusterCredits, clusterCreditsUnallocated,
        isUnitMode, provAppCreditPlanned, appUnitPerAgent, agentCount, creditIsValid])

    return (
        <VStack>
            <TableType2 dataArray={dataObject} className={styles.table} />
        </VStack>
    )
}

export default AppCredit

AppCredit.propTypes = {
  config: PropTypes.shape({
    cloud18ApplicationCredits: PropTypes.number,
    cloud18ApplicationCreditsUsed: PropTypes.number,
    cloud18ApplicationCreditsPlanned: PropTypes.number,
    provAppSizingMode: PropTypes.string,
  }),
  appConfig: PropTypes.shape({
    provAppCreditPlanned: PropTypes.number,
    provAppAgents: PropTypes.oneOfType([PropTypes.array, PropTypes.string]),
    provAppSizingMode: PropTypes.string,
  }),
}
