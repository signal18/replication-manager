import { VStack, HStack, Text, Input, Select, Heading, Flex, Box } from '@chakra-ui/react'
import { HiTrash } from 'react-icons/hi'
import RMIconButton from '../../../../../../components/RMIconButton';
import RMButton from '../../../../../../components/RMButton';
import styles from '../styles.module.scss';
import React, { useMemo, useState } from 'react';
import { DataTable } from '../../../../../../components/DataTable';
import { createColumnHelper } from '@tanstack/react-table';
import { uniqueId } from 'lodash';
import { useConditionalHandlers } from './Conditional';
import { VariableForm } from './VariableForm';

const initVariable = {
  name: "",
  type: "secret",
  value: "",
  conditional: [],
  locked: false, // Indicates if the variable is locked and cannot be edited
}

const columnHelper = createColumnHelper()

const maskString = (str, mask = '*') => {
  return str.replaceAll(/./g, mask)
}

export default React.memo(function Variables({
  rows = [],
  fieldName = "variables",
  agentList,
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
  onPauseAutoReload = () => { },
  onResumeAutoReload = () => { },
}) {
  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => (prevState.map((item, i) => i === index ? { ...item, [key]: value } : item)));
  };

  const handleAddItem = () => {
    setFormData(prevState => ([...prevState, { ...initVariable, id: uniqueId() }]));
    onPauseAutoReload(); // Pause auto-reload when adding a new item
  };

  const handleRemoveItem = (index) => {
    setFormData(prevState => {
      const newState = prevState.filter((_, i) => i !== index);
      if (newState.length === 0) {
        onResumeAutoReload(); // Resume auto-reload when no items left
      }
      return newState;
    });
  };

  const handleSaveAdd = () => {
    if (formData.length > 0) {
      onSaveAdd(fieldName, formData).then(() => {
        setFormData([]); // Clear the form after saving
        onResumeAutoReload(); // Resume auto-reload after saving
      })
    }
  }

  const agentOptions = useMemo(() => {
    if (!agentList) {
      return [];
    }

    // if string, split by comma
    if (typeof agentList === 'string') {
      // if empty string, return empty array
      if (agentList.trim() === '') {
        return [];
      }
      // split by comma and trim each agent
      agentList = agentList.split(',').map(agent => agent.trim());
    }
    return agentList.map(agent => ({ value: agent, name: agent }));
  }, [agentList]);


  const baseColumns = useMemo(
    () => [
      columnHelper.accessor((row) => row.name, {
        header: 'Name'
      }),
      columnHelper.accessor((row) => row.type, {
        header: 'Type'
      }),
      columnHelper.accessor((row) => row.type == "secret" ? maskString(row.value) : row.value, {
        header: 'Value'
      }),
      columnHelper.accessor((row) => row.conditional, {
        header: 'Conditional',
        cell: ({ row, getValue }) => {
          const conditional = getValue();
          if (!conditional || conditional.length === 0) {
            return <Text fontStyle="italic" color="gray.500">-</Text>;
          }
          return (
            <VStack align="start" spacing={1}>
              {conditional.map((item) => (
                <HStack key={item.agent} spacing={2}>
                  <Text>{item.agent}:</Text>
                  <Text fontWeight="bold">{row.original.type == "secret" ? maskString(item.value) : item.value}</Text>
                </HStack>
              ))}
            </VStack>
          );
        }
      }),
    ],
    []
  )

  const columnsRowForm = useMemo(
    () => [
      ...baseColumns,
      columnHelper.display({
        id: 'actions',
        cell: ({ row }) => (
          <RMIconButton
            icon={HiTrash}
            aria-label="Delete Variable"
            onClick={() => onRowDropIndex(fieldName, row.index)}
            isDisabled={row.original.locked}
          />
        ),
      }),
      {
        id: 'expansion',
        header: '',
        meta: {
          renderExpansion: (row) => {
            return (<VariableRowForm fieldName={fieldName} variable={row.original} agentOptions={agentOptions} index={row.index} onChange={onRowArrayChange} isDisabled={row.original.locked} />);
          },
        },
        cell: () => null,
      }
    ],
    [fieldName, agentOptions, onRowArrayChange, onRowDropIndex]
  )

  const columnsNewForm = useMemo(
    () => [
      ...baseColumns,
      columnHelper.display({
        id: 'actions',
        cell: ({ row }) => (
          <RMIconButton
            icon={HiTrash}
            aria-label="Delete Variable"
            onClick={() => handleRemoveItem(row.index)}
          />
        ),
      }),
      {
        id: 'expansion',
        header: '',
        meta: {
          renderExpansion: (row) => {
            return (<VariableNewForm variable={row.original} agentOptions={agentOptions} index={row.index} onChange={handleArrayChange} />);
          },
        },
        cell: () => null,
      }
    ],
    [agentOptions, handleArrayChange, handleRemoveItem]
  )

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Heading as="h3" size="md">
          Saved Variables
        </Heading>
        <Box className={styles.tableContainer}>
          <DataTable key="app-variables" data={rows} columns={columnsRowForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
        </Box>
      </VStack>
      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            Add New Variables
          </Heading>
          <Text>Enter variables to be used in the deployment. Choose type as Secret or Env.</Text>
          <Box className={styles.tableContainer}>
            <DataTable key="new-app-variables" data={formData} columns={columnsNewForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
          </Box>
        </VStack>
      )}
      <VStack spacing={3} align="stretch">
        <HStack>
          {formData.length > 0 && (
            <RMButton onClick={handleSaveAdd}>
              Save Variables
            </RMButton>
          )}
          <RMButton onClick={handleAddItem}>
            Add Variable
          </RMButton>
        </HStack>
      </VStack>
    </Flex>
  )
})

const VariableRowForm = React.memo(({ fieldName, variable, agentOptions, index, onChange, isDisabled }) => {
  const v = variable || { name: "", type: "secret", value: "", conditional: [], locked: false };
  const conditional = useMemo(() => Array.isArray(v.conditional) ? v.conditional : [], [v.conditional]);

  const { onAgentCheckboxChange, onConditionalValueChange } = useConditionalHandlers({
    conditional, index, onChange, fieldName
  });

  const renderAgentValue = useCallback((item) => {
      const agent = conditional.find(a => a.agent === item.value);
      if (!agent) return null;
  
      return (
        <AgentValueField
          key={item.value}
          type={v.type}
          agent={item.value}
          index={index}
          value={agent.value}
          onChange={onConditionalValueChange}
          editMode={"inline"}
        />
      );
    }, [v.type, conditional, index, onConditionalValueChange]);

  return (
    <VariableForm
      index={index}
      v={v}
      fieldName={fieldName}
      agentOptions={agentOptions}
      conditional={conditional}
      onChange={onChange}
      onAgentCheckboxChange={onAgentCheckboxChange}
      renderCheckedContent={renderAgentValue}
      isDisabled={isDisabled}
      renderType="form"
      styles={styles}
    />
  );
});

const VariableNewForm = React.memo(({ variable, agentOptions, index, onChange }) => {
  const [v, setV] = useState(variable || { name: "", type: "secret", value: "", conditional: [], locked: false });
  const conditional = useMemo(() => Array.isArray(v.conditional) ? v.conditional : [], [v.conditional]);

  const handleChange = (index, key, value) => {
    setV(prev => ({ ...prev, [key]: value }));
    onChange(index, key, value);
  };

  const { onAgentCheckboxChange, onConditionalValueChange } = useConditionalHandlers({
    conditional, index, onChange: (_, idx, key, val) => handleChange(idx, key, val), fieldName: null
  });

  const renderAgentValue = useCallback((item) => {
      const agent = conditional.find(a => a.agent === item.value);
      if (!agent) return null;
  
      return (
        <AgentValueField
          key={item.value}
          type={v.type}
          agent={item.value}
          index={index}
          value={agent.value}
          onChange={onConditionalValueChange}
          editMode={"inline"}
        />
      );
    }, [v.type, conditional, index, onConditionalValueChange]);

  return (
    <VariableForm
      index={index}
      v={v}
      fieldName={null}
      agentOptions={agentOptions}
      conditional={conditional}
      onChange={handleChange}
      onAgentCheckboxChange={onAgentCheckboxChange}
      onConditionalValueChange={onConditionalValueChange}
      renderCheckedContent={renderAgentValue}
      renderType="inline"
      styles={styles}
    />
  );
});

