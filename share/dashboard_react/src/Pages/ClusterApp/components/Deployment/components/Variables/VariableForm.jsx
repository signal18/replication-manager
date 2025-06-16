import { Flex, Text, Input, Select } from "@chakra-ui/react";
import { AgentValueField } from "./Conditional";
import Checkboxes from "../../../../../../components/Checkboxes/Checkboxes";
import TextForm from "../../../../../../components/TextForm";
import PasswordControl from "../../../../../../components/PasswordControl";
import Dropdown from "../../../../../../components/Dropdown";
import { useMemo } from "react";

const defaultConfirmText = "Are you sure you want to change this field to: ";

const variableTypes = [
  { value: 'secret', name: 'Secret' },
  { value: 'env', name: 'Env' },
]

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

  return agentOptions.map(item => ({ value: item.value, name: item.name, renderCheckedContent: renderCheckedContent }));
}


export const VariableForm = ({
  index, v, fieldName, agentOptions, conditional,
  onChange, onAgentCheckboxChange, onConditionalValueChange,
  renderType = "form", isDisabled = false, styles
}) => {
  const fieldPrefix = `variables[${index}]`;

  const renderAgentValue = (item) => {
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
        editMode={renderType === "inline" ? "inline" : "form"}
      />
    );
  };

  const agentList = useMemo(() => {
    return buildAgentCheckboxOptions(agentOptions, renderAgentValue);
  }, [agentOptions, conditional]);

  if (isDisabled) {
    return <Text fontWeight="bold">Variable is locked. Please change the source configuration.</Text>;
  }

  return (
    <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
      <Flex direction="column" flex="1" minW="300px" gap={2}>
        <Flex direction="column">
          <Text mb={1}>Variable Name:</Text>
          {renderType === "inline" ? (
            <Input
              name={`${fieldPrefix}.name`}
              placeholder="Name"
              value={v.name}
              onChange={(e) => onChange(index, "name", e.target.value)}
            />
          ) : (
            <TextForm name={`${fieldPrefix}.name`} placeholder="Name" value={v.name} onSave={(val) => onChange(fieldName, index, "name", val)} confirmTitle={defaultConfirmText} />
          )}
        </Flex>

        <Flex direction="column">
          <Text mb={1}>Variable Type:</Text>
          {renderType === "inline" ? (
            <Select value={v.type} onChange={(e) => onChange(index, "type", e.target.value)}>
              {variableTypes.map(type => <option key={type.value} value={type.value}>{type.name}</option>)}
            </Select>
          ) : (
            <Dropdown id={`${fieldPrefix}.type`} selectedValue={v.type} onChange={(e) => onChange(fieldName, index, "type", e.target.value)} options={variableTypes} confirmTitle="Change variable type?" />
          )}
        </Flex>

        <Flex direction="column">
          <Text mb={1}>Variable Value:</Text>
          {renderType === "inline" ? (
            v.type === "secret" ? (
              <PasswordControl noControl={true} name={`${fieldPrefix}.secret`} placeholder="Secret" value={v.value} onChange={(e) => onChange(index, "value", e.target.value)} />
            ) : (
              <Input name={`${fieldPrefix}.env`} placeholder="Env" value={v.value} onChange={(e) => onChange(index, "value", e.target.value)} />
            )
          ) : (
            v.type === "secret" ? (
              <TextForm name={`${fieldPrefix}.secret`} confirmTitle={defaultConfirmText} type="password" placeholder="Secret" value={v.value} onSave={(val) => onChange(fieldName, index, "value", val)} />
            ) : (
              <TextForm name={`${fieldPrefix}.env`} confirmTitle={defaultConfirmText}  placeholder="Env" value={v.value} onSave={(val) => onChange(fieldName, index, "value", val)} />
            )
          )}
        </Flex>
      </Flex>

      <Flex direction="column" flex="1" minW="200px">
        <Text mb={1}>Conditional:</Text>
        <Checkboxes
          list={agentList}
          values={conditional.map(i => i.agent)}
          onChange={(val) => onAgentCheckboxChange(val, v)}
          direction="column"
          parentStyles={styles}
          confirm={renderType !== "inline"}
          confirmTitle="Are you sure to modify the conditional agents?"
          confirmBodyTitle="Conditional agents changed to:"
        />
      </Flex>
    </Flex>
  );
};
