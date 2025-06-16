import { Input } from "@chakra-ui/react";
import { useCallback } from "react";
import { shallowEqual } from "react-redux";
import { useTheme } from "../../../../../../ThemeProvider";
import PasswordControl from "../../../../../../components/PasswordControl";
import TextForm from "../../../../../../components/TextForm";

export const AgentValueField = ({ type, agent, index, value, onChange, editMode }) => {
  const { theme } = useTheme();
  const name = `variables[${index}].conditional.${agent}.${type}`;

  if (editMode === "inline") {
    return type === "secret" ? (
      <PasswordControl
        noControl={true}
        inputClassName={theme === 'dark' ? "darkLoginText" : ""}
        labelClassName={theme === 'dark' ? "darkLoginText" : ""}
        name={name}
        placeholder="Secret"
        value={value}
        onChange={(e) => onChange(agent, e.target.value)}
      />
    ) : (
      <Input name={name} placeholder="Env" value={value} onChange={(e) => onChange(agent, e.target.value)} />
    );
  }

  return type === "secret" ? (
    <TextForm key={name} name={name} type="password" value={value} onSave={(val) => onChange(agent, val)} />
  ) : (
    <TextForm key={name} name={name} value={value} onSave={(val) => onChange(agent, val)} />
  );
};

export function useConditionalHandlers({ conditional = [], index, onChange, fieldName }) {
  const onAgentCheckboxChange = useCallback((checkeds, defaultValue) => {
    const list = checkeds.split(",").map(agent => agent.trim());
    const oldList = conditional.map(item => item.agent);

    if (shallowEqual(list, oldList)) return;
    if (list.length === 0) {
      onChange(fieldName, index, "conditional", []);
      return;
    }

    const updatedAgents = list.map(agent => {
      const existing = conditional.find(item => item.agent === agent);
      return { agent, value: existing?.value ?? defaultValue };
    });

    onChange(fieldName, index, "conditional", updatedAgents);
  }, [conditional, fieldName, index, onChange]);

  const onConditionalValueChange = useCallback((agent, value) => {
    const updated = conditional.map(item =>
      item.agent === agent ? { ...item, value } : item
    );
    onChange(fieldName, index, "conditional", updated);
  }, [conditional, fieldName, index, onChange]);

  return { onAgentCheckboxChange, onConditionalValueChange };
}
