import React, { useState } from 'react'
import { Box, Button, HStack, Text, Badge, useToast, Textarea, VStack } from '@chakra-ui/react'
import { useDispatch } from 'react-redux'
import { executeQuery } from '../../redux/clusterSlice'
import { DataTable } from '../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import AccordionComponent from '../../components/AccordionComponent'
import RMTextarea from '../../components/RMTextarea'
import styles from './styles.module.scss'

function SqlQuery({ selectedCluster }) {
  const dispatch = useDispatch()
  const toast = useToast()
  const [query, setQuery] = useState('')
  const [results, setResults] = useState(null)
  const [isExecuting, setIsExecuting] = useState(false)
  const [executedVia, setExecutedVia] = useState(null)

  const handleExecuteQuery = async () => {
    if (!query.trim()) {
      toast({
        title: 'Empty Query',
        description: 'Please enter a SQL query to execute',
        status: 'warning',
        duration: 3000,
        isClosable: true,
      })
      return
    }

    setIsExecuting(true)
    try {
      const response = await dispatch(
        executeQuery({ clusterName: selectedCluster?.name, query: query.trim() })
      ).unwrap()

      if (response?.data) {
        setResults(response.data.results || [])
        setExecutedVia(response.data.executedVia || null)
      }
    } catch (error) {
      console.error('Query execution error:', error)
      setResults(null)
      setExecutedVia(null)
    } finally {
      setIsExecuting(false)
    }
  }

  const handleClearQuery = () => {
    setQuery('')
    setResults(null)
    setExecutedVia(null)
  }

  const columnHelper = createColumnHelper()

  // Dynamically generate columns based on results
  const columns = React.useMemo(() => {
    if (!results || results.length === 0) return []

    const firstRow = results[0]
    return Object.keys(firstRow).map((key) =>
      columnHelper.accessor((row) => row[key], {
        header: key,
        cell: (info) => {
          const value = info.getValue()
          if (value === null || value === undefined) {
            return <Text color="gray.400">NULL</Text>
          }
          return <Text>{String(value)}</Text>
        }
      })
    )
  }, [results])

  const queryInputBody = (
    <VStack className={styles.queryInputSection} align="stretch" spacing={6}>
      {/* Query Input Section */}
      <Box className={styles.formSection}>
        <HStack justifyContent="space-between" mb={3}>
          <Text fontSize="sm" fontWeight="600" textTransform="uppercase" color="var(--text-color)">
            Query Input
          </Text>
          {executedVia && (
            <Badge colorScheme={executedVia === 'proxy' ? 'green' : 'blue'} fontSize="xs">
              Executed via: {executedVia}
            </Badge>
          )}
        </HStack>
        
        <RMTextarea
          placeholder="Enter your SQL query here... (e.g., SELECT * FROM mysql.user LIMIT 10)"
          value={query}
          handleInputChange={(e) => setQuery(e.target.value)}
          rows={8}
        />
      </Box>

      {/* Action Buttons Section */}
      <Box className={styles.actionsSection}>
        <HStack spacing={3} wrap="wrap">
          <Button
            colorScheme="blue"
            size="md"
            onClick={handleExecuteQuery}
            isLoading={isExecuting}
            loadingText="Executing..."
            isDisabled={!query.trim()}
          >
            Execute Query
          </Button>
          <Button
            colorScheme="gray"
            variant="outline"
            size="md"
            onClick={handleClearQuery}
            isDisabled={isExecuting}
          >
            Clear
          </Button>
        </HStack>
      </Box>

      {/* Info Section */}
      <Box className={styles.infoSection}>
        <Text fontSize="xs" fontWeight="500" mb={1} textTransform="uppercase" color="var(--text-color)">
          Information
        </Text>
        <Box className={styles.queryHint}>
          <Text fontSize="xs" color="gray.500">
            Queries are executed via proxy if available, otherwise directly on the master. Use with caution as queries can impact cluster performance.
          </Text>
        </Box>
      </Box>
    </VStack>
  )

  const resultsBody = results !== null && results.length > 0 ? (
    <DataTable
      key="query-results"
      data={results}
      columns={columns}
      className={styles.table}
    />
  ) : results !== null ? (
    <Box className={styles.emptyResults}>
      <Text color="gray.500">Query executed successfully. No results returned.</Text>
    </Box>
  ) : null

  return (
    <>
      <AccordionComponent
        heading={'SQL QUERY EXECUTION'}
        allowToggle={false}
        className={styles.accordion}
        panelSX={{ overflowX: 'auto', p: 3 }}
        body={queryInputBody}
      />
      {results !== null && (
        <AccordionComponent
          heading={`QUERY RESULTS (${results.length} row${results.length !== 1 ? 's' : ''})`}
          allowToggle={false}
          className={styles.accordion}
          panelSX={{ overflowX: 'auto', p: 0 }}
          body={resultsBody}
        />
      )}
    </>
  )
}

export default SqlQuery
