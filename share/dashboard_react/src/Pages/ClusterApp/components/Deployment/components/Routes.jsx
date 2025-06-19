import { VStack, HStack, Text, Heading, Input, Select, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiTrash } from 'react-icons/hi'
import TextForm from '../../../../../components/TextForm';
import RMIconButton from '../../../../../components/RMIconButton';
import RMButton from '../../../../../components/RMButton';
import styles from './styles.module.scss';
import { uniqueId } from 'lodash';
import Dropdown from '../../../../../components/Dropdown';

const defaultConfirmText = "Are you sure to change this field to: ";

const protocolOptions = [
  { value: 'https', name: 'HTTPS' },
  { value: 'tcp', name: 'TCP' },
];

export default React.memo(function Routes({
  rows = [],
  fieldName = 'routes',
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
  onPauseAutoReload = () => { },
  onResumeAutoReload = () => { },
}) {

  const [formData, setFormData] = useState([]);

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => [...prevState.map((item, i) => i === index ? { ...item, [key]: value } : item)]);
  };

  const handleAddItem = () => {
    setFormData(prevState => [...prevState, { id: uniqueId(), cname: "", port: "", protocol: "https" }]);
    onPauseAutoReload(); // Pause auto-reload when adding a new item
  };

  const handleRemoveItem = (index) => {
    setFormData(prevState => {
      const newState = [...prevState.filter((_, i) => i !== index)];
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

  const sanitizePort = (port) => {
    const sanitizedPort = parseInt(port, 10);
    return isNaN(sanitizedPort) || sanitizedPort < 1 ? "" : sanitizedPort > "65535" ? "65535" : `${sanitizedPort}`;
  }

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Heading as="h3" size="md">
          Saved route mappings
        </Heading>
        <Text>These are the route mappings that will be used for your deployment. Make sure the domain already points to our gateway.</Text>
        {rows?.length > 0 ?
          rows?.map((p, index) => (
            <HStack key={`row_${p.port}`}>
              <TextForm confirmTitle={defaultConfirmText} name={`row_${p.port}.cname`} placeholder="CNAME" value={p.cname} onSave={(value) => onRowArrayChange(fieldName, index, "cname", value)} />
              <TextForm confirmTitle={defaultConfirmText} pattern='^[0-9]{1,5}$' name={`row_${p.port}.port`} placeholder="Port" value={p.port} onSave={(value) => onRowArrayChange(fieldName, index, "port", sanitizePort(value))} />
              <Dropdown confirmTitle={defaultConfirmText} name={`row_${p.port}.volumedir`} selectedValue={p.protocol} onChange={(value) => onRowArrayChange(fieldName, index, "protocol", value)} options={protocolOptions} />
              <RMIconButton icon={HiTrash} aria-label="Delete Route" onClick={() => onRowDropIndex(fieldName, index)} />
            </HStack>
          )) : (
            <Text>No saved route mappings</Text>
          )}
      </VStack>
      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">
            New Route Mapping
          </Heading>
          <Text>Enter the route mappings for your deployment. Select a volume directory and specify the source and destination Routes.</Text>
          {formData.map((p, index) => (
            <HStack key={`new_${p.id}`}>
              <Input name={`new_${p.id}.cname`} placeholder="CNAME" value={p.cname} onChange={(e) => handleArrayChange(index, "cname", e.target.value)} />
              <Input name={`new_${p.id}.port`} pattern='^[0-9]{1,5}$' placeholder="Port" value={p.port} onChange={(e) => handleArrayChange(index, "port", sanitizePort(e.target.value))} />
              <Select value={p.protocol} onChange={(e) => handleArrayChange(index, "protocol", e.target.value)} >
                {protocolOptions.map(option => (
                  <option key={option.value} value={option.value}>
                    {option.name}
                  </option>
                ))}
              </Select>
              <RMIconButton icon={HiTrash} aria-label="Delete Route" onClick={() => handleRemoveItem(index)} />
            </HStack>
          ))}
        </VStack>
      )}
      <VStack spacing={3} align="stretch">
        <HStack>
          {formData?.length > 0 && (
            <RMButton onClick={handleSaveAdd}>
              Save Route
            </RMButton>
          )}
          <RMButton onClick={handleAddItem}>
            Add Route
          </RMButton>
        </HStack>
      </VStack>
    </Flex>
  )
})
