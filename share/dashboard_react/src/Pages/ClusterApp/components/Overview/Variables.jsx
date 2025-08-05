import { VStack, HStack, Text, Input, Select, Heading, Flex, Box } from '@chakra-ui/react'
import { HiTrash } from 'react-icons/hi'
import TextForm from '../../../../components/TextForm';
import Dropdown from '../../../../components/Dropdown';
import RMIconButton from '../../../../components/RMIconButton';
import RMButton from '../../../../components/RMButton';
import styles from './styles.module.scss';
import React, { useCallback, useMemo, useState } from 'react';
import { DataTable } from '../../../../components/DataTable';
import { createColumnHelper } from '@tanstack/react-table';
import Checkboxes from '../../../../components/Checkboxes/Checkboxes';
import { uniqueId } from 'lodash';
import PasswordControl from '../../../../components/PasswordControl';
import { useTheme } from '../../../../ThemeProvider';
import VariableInputArea from '../../../../components/VariableTree/VariableInputArea';

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
  onResolveVariable = () => { },
  onPauseAutoReload = () => { },
  onResumeAutoReload = () => { },
}) {
  const [isVisible, setIsVisible] = useState(false);

  const handleAddItem = () => {
      setIsVisible(true);
      onPauseAutoReload(); // Pause auto-reload when adding a new item
    };
  
    const handleCancel = useCallback(() => {
      setIsVisible(false);
      onResumeAutoReload();
    }, [onResumeAutoReload]);

    const handleResolve = useCallback((rawValue) => {
      return onResolveVariable(rawValue)
    }, [onResolveVariable]);
  
    const handleSaveAdd = useCallback((formData) => {
      return onSaveAdd(fieldName, formData).then(() => {
        setIsVisible(false);
        onResumeAutoReload();
        return Promise.resolve();
      }, (error) => {
        return Promise.reject(error);
      });
    }, [onSaveAdd, fieldName, onResumeAutoReload]);

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


  const columnsRowForm = useMemo(
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
            return (<VariableRowForm fieldName={fieldName} variable={row.original} agentOptions={agentOptions} index={row.index} onChange={onRowArrayChange} isDisabled={row.original.locked} substitution={substitution} onResolveVariable={handleResolve} />);
          },
        },
        cell: () => null,
      }
    ],
    [fieldName, agentOptions, onRowArrayChange, onRowDropIndex, substitution]
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
      {isVisible ? (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            Add New Variable
          </Heading>
          <Box className={styles.tableContainer}>
            <VariableNewForm agentOptions={agentOptions} substitution={substitution} onSave={handleSaveAdd} onCancel={handleCancel} onResolveVariable={handleResolve} />
          </Box>
        </VStack>
      ) : (
        <VStack spacing={3} align="stretch">
          <HStack>
            <RMButton onClick={handleAddItem}>
              Add Variable
            </RMButton>
          </HStack>
        </VStack>
      )}
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

const VariableRowForm = React.memo(({ fieldName, variable, agentOptions, index, onChange, isDisabled, substitution, onResolveVariable }) => {
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
        <TextForm key={`variables[${index}].conditional.${item.value}.secret`} confirmTitle={"Variable value changed"} name={`variables[${index}].conditional.${item.value}.secret`} type="password" placeholder="Secret" value={agentExists.value} onSave={(value) => onConditionalValueChange(item.value, value)} />
      );
    }

    return (
      <VariableInputArea variables={substitution} key={`variables[${index}].conditional.${item.value}.env`} useConfirmModal={true} confirmTitle={"Variable value changed"} name={`variables[${index}].conditional.${item.value}.env`} placeholder="Env" value={agentExists.value} onSave={(value) => onConditionalValueChange(item.value, value)} onResolveVariable={onResolveVariable} />
    );
  }, [index, onConditionalValueChange, conditional, substitution, onResolveVariable]);

  const agentList = useMemo(() => {
    return buildAgentCheckboxOptions(agentOptions, renderAgentValue);
  }, [agentOptions, conditional, renderAgentValue, substitution]);

  // Just return that variable is locked
  if (isDisabled) {
    return (<Text fontWeight="bold">Variable is locked. Please change the source configuration.</Text>);
  }

  return (
    <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Name:</Text>
          <TextForm confirmTitle={"Variable name changed"} name={`variables[${index}].name`} placeholder="Name" value={v.name} onSave={(value) => onRowArrayChange(fieldName, index, "name", value)} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Type:</Text>
          <Dropdown id={`variables[${index}].type`} confirmTitle={"Variable type changed"} selectedValue={v.type} onChange={(e) => onRowArrayChange(fieldName, index, "type", e.target.value)} options={variableTypes} />
        </Flex>
        <Flex direction="column" flex="1">
          <Text mb={1}>Variable Value:</Text>
          {v.type === "secret" ? (
            <TextForm confirmTitle={"Variable value changed"} name={`variables[${index}].secret`} type="password" placeholder="Secret" value={v.value} onSave={(value) => onRowArrayChange(fieldName, index, "value", value)} />
          ) : (
            <VariableInputArea variables={substitution} useConfirmModal={true} confirmTitle={"Variable value changed"} name={`variables[${index}].env`} placeholder="Env" value={v.value} onSave={(value) => onRowArrayChange(fieldName, index, "value", value)} onResolveVariable={onResolveVariable} />
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

const VariableNewForm = React.memo(({ agentOptions, substitution, onSave, onCancel, onResolveVariable }) => {
  const [v, setV] = useState(initVariable);
  const { theme } = useTheme();
  const { name, type, value } = v;

  const valid = useMemo(() => {
    return name && type 
  }, [name, type]);

  const conditional = useMemo(() => {
    if (!v.conditional || !Array.isArray(v.conditional)) {
      return [];
    }
    return v.conditional;
  }, [v.conditional]);

  const handleResolve = useCallback((rawValue) => {
    return onResolveVariable(rawValue)
  }, [onResolveVariable]);

  const handleArrayChange = useCallback((key, value) => {
    setV((prev) => ({ ...prev, [key]: value }));
  }, [])

  const onAgentCheckboxChange = useCallback((checkeds, cstate) => {
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
    handleArrayChange("conditional", updatedAgents);
  }, [handleArrayChange]);

  const onConditionalValueChange = useCallback((agent, value) => {
    const updatedAgents = conditional.map(item => item.agent === agent ? { ...item, value } : item);
    handleArrayChange("conditional", updatedAgents);
  }, [conditional, handleArrayChange]);

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
          name={`variable.conditional.${item.value}.secret`}
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
        name={`variable.conditional.${item.value}.env`}
        placeholder="Env"
        value={agentExists.value}
        onChange={(value) => onConditionalValueChange(item.value, value)}
        onResolveVariable={handleResolve}
      />
    )
  }, [onConditionalValueChange, conditional, substitution, handleResolve]);

  const agentList = useMemo(() => {
    return buildAgentCheckboxOptions(agentOptions, renderAgentValue);
  }, [agentOptions, conditional, renderAgentValue, substitution]);

  const handleSaveAdd = useCallback(() => {
      if (valid) {
        onSave([v]);
      }
    }, [onSave, valid, v]);
  
    const handleCancel = useCallback(() => {
      setV(initVariable);
      onCancel();
    }, [onCancel]);

  return (
    <Flex direction={"column"} gap={4}>
      <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
        <Flex direction="column" flex="1" minW="300px" gap={2}>
          <Flex direction="column" flex="1">
            <Text mb={1}>Variable Name:</Text>
            <Input
              name={`variable.name`}
              placeholder="Name"
              value={name}
              onChange={(e) => handleArrayChange("name", e.target.value)}
            />
          </Flex>
          <Flex direction="column" flex="1">
            <Text mb={1}>Variable Type:</Text>
            <Select
              value={type}
              onChange={(e) => handleArrayChange("type", e.target.value)}
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
                name={`variable.secret`}
                placeholder="Secret"
                value={value}
                onChange={(e) => handleArrayChange("value", e.target.value)}
              />
            ) : (
              <VariableInputArea
                variables={substitution}
                alwaysEditable={true}
                name={`variable.env`}
                placeholder="Env"
                value={value}
                onChange={(value) => handleArrayChange("value", value)}
                onResolveVariable={handleResolve}
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
      <Flex direction="column" flex="1">
        <HStack spacing={2} mt={4}>
          <RMButton onClick={handleCancel}>
            Clear Form
          </RMButton>
          <RMButton onClick={handleSaveAdd} isDisabled={!valid}>
            Save Variables
          </RMButton>
        </HStack>
      </Flex>
    </Flex>
  )
})