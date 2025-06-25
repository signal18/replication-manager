import { VStack, HStack, Text, Input, Select, Heading, Flex, Box } from '@chakra-ui/react'
import { HiTrash } from 'react-icons/hi'
import TextForm from '../../../../../components/TextForm';
import Dropdown from '../../../../../components/Dropdown';
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';
import React, { useCallback, useMemo, useState } from 'react';
import { DataTable } from '../../../../../components/DataTable';
import { createColumnHelper } from '@tanstack/react-table';
import Checkboxes from '../../../../../components/Checkboxes/Checkboxes';
import { uniqueId } from 'lodash';
import PasswordControl from '../../../../../components/PasswordControl';
import { useTheme } from '../../../../../ThemeProvider';
import VariableInputArea from '../../../../../components/VariableTree/VariableInputArea';

const defaultConfirmText = "Are you sure you want to change this field to: ";

const variableTypes = [
  { value: 'secret', name: 'Secret' },
  { value: 'env', name: 'Env' },
]

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
  substitution,
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
            return (<VariableRowForm fieldName={fieldName} variable={row.original} agentOptions={agentOptions} index={row.index} onChange={onRowArrayChange} isDisabled={row.original.locked} substitution={substitution}/>);
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
            return (<VariableNewForm variable={row.original} agentOptions={agentOptions} index={row.index} onChange={handleArrayChange} substitution={substitution}/>);
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

function buildAgentCheckboxOptions(agentOptions, renderCheckedContent) {
  if (!agentOptions || agentOptions.length === 0) {
    return [];
  }
  if (typeof agentOptions === 'string') {
    // if empty string, return empty array
    if (agentOptions.trim() === '') {
      return [];
    }
    // split by comma and trim each agent
    agentOptions = agentOptions.split(',').map(agent => ({ value: agent.trim(), name: agent.trim() }));
  }

  return agentOptions.map(item => ({ value: item.value, name: item.name, renderCheckedContent: renderCheckedContent })).sort((a, b) => a.name.localeCompare(b.name));
}

const VariableRowForm = React.memo(({ fieldName, variable, agentOptions, index, onChange, isDisabled, substitution }) => {
  const v = variable || { name: "", type: "secret", value: "", conditional: [], locked: false };

  const onRowArrayChange = (fieldName, index, key, value) => {
    onChange(fieldName, index, key, value);
  };

  const conditional = useMemo(() => {
    if (!v.conditional || !Array.isArray(v.conditional)) {
      return [];
    }
    return v.conditional;
  }, [v.conditional]);

  const onAgentCheckboxChange = (checkeds, cstate) => {
    if (!Array.isArray(checkeds)) {
      checkeds = checkeds.split(",").map(agent => agent.trim());
    }

    // If no agents checked, set conditional to empty array
    const updatedAgents = checkeds.length > 0
      ? checkeds.map(agent => {
        const existing = cstate.conditional?.find(item => item.agent === agent);
        return existing ? existing : { agent, value: cstate.value };
      })
      : []; // If no agents checked, set to empty array
    onRowArrayChange(fieldName, index, "conditional", updatedAgents);
  };

  const onConditionalValueChange = useCallback((agent, value) => {
    const updatedAgents = conditional.map(item => item.agent === agent ? { ...item, value } : item);
    onRowArrayChange(fieldName, index, "conditional", updatedAgents);
  }, [conditional, index, onRowArrayChange]);

  const renderAgentValue = useCallback((item) => {
    if (!conditional || !Array.isArray(conditional)) {
      return null; // Ensure conditional is an array before proceeding
    }
    // Check if the agent exists in the conditional array
    const agentExists = conditional?.find(agent => agent.agent === item.value);
    if (!agentExists) {
      return null; // If the agent does not exist, return null
    }

    if (v.type === "secret") {
      return (
        <TextForm key={`variables[${index}].conditional.${item.value}.secret`} confirmTitle={defaultConfirmText} name={`variables[${index}].conditional.${item.value}.secret`} type="password" placeholder="Secret" value={agentExists.value} onSave={(value) => onConditionalValueChange(item.value, value)} />
      );
    }

    return (
      <VariableInputArea variables={substitution} key={`variables[${index}].conditional.${item.value}.env`} useConfirmModal={true} confirmTitle={defaultConfirmText} name={`variables[${index}].conditional.${item.value}.env`} placeholder="Env" value={agentExists.value} onSave={(value) => onConditionalValueChange(item.value, value)} />
    );
  }, [index, onConditionalValueChange, conditional]);

  const agentList = useMemo(() => {
    return buildAgentCheckboxOptions(agentOptions, renderAgentValue);
  }, [agentOptions, conditional, renderAgentValue]);

  // Just return that variable is locked
  if (isDisabled) {
    return (<Text fontWeight="bold">Variable is locked. Please change the source configuration.</Text>);
  }

  return (
    <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Name:</Text>
          <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].name`} placeholder="Name" value={v.name} onSave={(value) => onRowArrayChange(fieldName, index, "name", value)} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Type:</Text>
          <Dropdown id={`variables[${index}].type`} confirmTitle={"Are you sure to change variable type: "} selectedValue={v.type} onChange={(e) => onRowArrayChange(fieldName, index, "type", e.target.value)} options={variableTypes} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Value:</Text>
          {v.type === "secret" ? (
            <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].secret`} type="password" placeholder="Secret" value={v.value} onSave={(value) => onRowArrayChange(fieldName, index, "value", value)} />
          ) : (
            <VariableInputArea variables={substitution} useConfirmModal={true} confirmTitle={defaultConfirmText} name={`variables[${index}].env`} placeholder="Env" value={v.value} onSave={(value) => onRowArrayChange(fieldName, index, "value", value)} />
          )}
        </Flex>
      </Flex>
      <Flex direction="column" flex="1" minW="200px">
        <Text mb={1}>Conditional:</Text>
        <Checkboxes
          list={agentList}
          values={conditional.map(item => item.agent)}
          onChange={(value) => onAgentCheckboxChange(value, v)}
          parentStyles={styles}
          confirm={true}
          confirmTitle="Are you sure to modify the conditional agents?"
          confirmBodyTitle="Conditional agents changed to:"
          splitConfirm={false}
          direction="column"
        />
      </Flex>
    </Flex>
  )
})

const VariableNewForm = React.memo(({ variable, agentOptions, index, onChange, substitution }) => {
  const [v, setV] = useState(variable || { name: "", type: "secret", value: "", conditional: [], locked: false });
  const { theme } = useTheme();

  const conditional = useMemo(() => {
    if (!v.conditional || !Array.isArray(v.conditional)) {
      return [];
    }
    return v.conditional;
  }, [v.conditional]);

  const handleArrayChange = (index, key, value) => {
    setV((prev) => ({ ...prev, [key]: value }));
    // only send the change if the value is different and using debounce to avoid too many updates
    onChange(index, key, value);
  }

  const onAgentCheckboxChange = (checkeds, cstate) => {
    if (!Array.isArray(checkeds)) {
      checkeds = checkeds.split(",").map(agent => agent.trim());
    }

    // If no agents checked, set conditional to empty array
    const updatedAgents = checkeds.length > 0
      ? checkeds.map(agent => {
        const existing = cstate.conditional?.find(item => item.agent === agent);
        return existing ? existing : { agent, value: cstate.value };
      })
      : []; // If no agents checked, set to empty array
    handleArrayChange(index, "conditional", updatedAgents);
  };

  const onConditionalValueChange = useCallback((agent, value) => {
    const updatedAgents = conditional.map(item => item.agent === agent ? { ...item, value } : item);
    handleArrayChange(index, "conditional", updatedAgents);
  }, [conditional, index, handleArrayChange]);

  const renderAgentValue = useCallback((item) => {
    if (!conditional || !Array.isArray(conditional)) {
      return null; // Ensure conditional is an array before proceeding
    }
    // Check if the agent exists in the conditional array
    const agentExists = conditional.find(agent => agent.agent === item.value);
    if (!agentExists) {
      return null; // If the agent does not exist, return null
    }

    if (v.type === "secret") {
      return (
        <PasswordControl
          noControl={true}
          inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
          labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
          name={`variables[${index}].conditional.${item.value}.secret`}
          placeholder="Secret"
          value={agentExists.value}
          onChange={(e) => onConditionalValueChange(item.value, e.target.value)}
        />
      )
    }

    // If the type is not secret, render as input
    return (
      <VariableInputArea
        variables={substitution}
        alwaysEditable={true}
        name={`variables[${index}].conditional.${item.value}.env`}
        placeholder="Env"
        value={agentExists.value}
        onChange={(value) => onConditionalValueChange(item.value, value)}
      />
    )
  }, [index, onConditionalValueChange, conditional]);

  const agentList = useMemo(() => {
    return buildAgentCheckboxOptions(agentOptions, renderAgentValue);
  }, [agentOptions, conditional, renderAgentValue]);

  return (
    <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Name:</Text>
          <Input
            name={`variables[${index}].name`}
            placeholder="Name"
            value={v.name}
            onChange={(e) => handleArrayChange(index, "name", e.target.value)}
          />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Type:</Text>
          <Select
            value={v.type}
            onChange={(e) => handleArrayChange(index, "type", e.target.value)}
          >
            {variableTypes.map((type) => (
              <option key={type.value} value={type.value}>
                {type.name}
              </option>
            ))}
          </Select>
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Value:</Text>
          {v.type === "secret" ? (
            <PasswordControl
              noControl={true}
              inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
              labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
              name={`variables[${index}].secret`}
              placeholder="Secret"
              value={v.value}
              onChange={(e) => handleArrayChange(index, "value", e.target.value)}
            />
          ) : (
            <VariableInputArea
              variables={substitution}
              alwaysEditable={true}
              name={`variables[${index}].env`}
              placeholder="Env"
              value={v.value}
              onChange={(value) => handleArrayChange(index, "value", value)}
            />
          )}
        </Flex>
      </Flex>

      <Flex direction="column" flex="1" minW="200px">
        <Text mb={1}>Conditional:</Text>
        <Checkboxes
          list={agentList}
          values={conditional.map(item => item.agent)}
          onChange={(value) => onAgentCheckboxChange(value, v)}
          parentStyles={styles}
          direction="column"
        />
      </Flex>
    </Flex>
  )
})