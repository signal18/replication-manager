import React, { useState } from "react";
import {
  Modal,
  ModalOverlay,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Button,
  FormControl,
  FormLabel,
  Input,
  Select,
  VStack,
  HStack,
  Text,
  IconButton,
} from "@chakra-ui/react";
import { AddIcon, DeleteIcon } from "@chakra-ui/icons";

const DeploymentFormModal = ({ isOpen, onClose, onSubmit }) => {
  const [formData, setFormData] = useState({
    name: "",
    variables: [],
    path: [],
    ports: [],
    dockerImg: "",
    dockerRunArgs: "",
    dockerRunCmd: "",
    gitClones: [],
  });

  const [errors, setErrors] = useState({});

  const validateForm = () => {
    let newErrors = {};
    if (!formData.name) newErrors.name = "Name is required";
    if (!formData.dockerImg) newErrors.dockerImg = "Docker image is required";

    formData.variables.forEach((v, index) => {
      if (!v.name) newErrors[`variable-${index}`] = "Variable name is required";
    });

    formData.path.forEach((p, index) => {
      if (!p.from) newErrors[`path-from-${index}`] = "From path is required";
      if (!p.to) newErrors[`path-to-${index}`] = "To path is required";
    });

    formData.ports.forEach((p, index) => {
      if (!p.containerPort || isNaN(p.containerPort))
        newErrors[`port-${index}`] = "Valid port number is required";
    });

    formData.gitClones.forEach((g, index) => {
      if (!g.gitRepo) newErrors[`gitRepo-${index}`] = "Git repo is required";
    });

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };

  const handleInputChange = (field, value) => {
    setFormData({ ...formData, [field]: value });
  };

  const handleArrayChange = (field, index, key, value) => {
    setFormData({
      ...formData,
      [field]: formData[field].map((item, i) =>
        i === index ? { ...item, [key]: value } : item
      ),
    });
  };

  const handleAddItem = (field, newItem) => {
    setFormData({ ...formData, [field]: [...formData[field], newItem] });
  };

  const handleRemoveItem = (field, index) => {
    setFormData({
      ...formData,
      [field]: formData[field].filter((_, i) => i !== index),
    });
  };

  const handleSubmit = () => {
    if (validateForm()) {
      onSubmit(formData);
      onClose();
    }
  };

  return (
    <Modal isOpen={isOpen} onClose={onClose}>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>Deployment Form</ModalHeader>
        <ModalBody>
          <VStack spacing={4} align="stretch">
            <FormControl isInvalid={errors.name}>
              <FormLabel>Name</FormLabel>
              <Input
                value={formData.name}
                onChange={(e) => handleInputChange("name", e.target.value)}
              />
              {errors.name && <Text color="red.500">{errors.name}</Text>}
            </FormControl>

            <FormControl isInvalid={errors.dockerImg}>
              <FormLabel>Docker Image</FormLabel>
              <Input
                value={formData.dockerImg}
                onChange={(e) => handleInputChange("dockerImg", e.target.value)}
              />
              {errors.dockerImg && <Text color="red.500">{errors.dockerImg}</Text>}
            </FormControl>

            <FormControl>
              <FormLabel>Docker Run Args</FormLabel>
              <Input
                value={formData.dockerRunArgs}
                onChange={(e) => handleInputChange("dockerRunArgs", e.target.value)}
              />
            </FormControl>

            <FormControl>
              <FormLabel>Docker Run Cmd</FormLabel>
              <Input
                value={formData.dockerRunCmd}
                onChange={(e) => handleInputChange("dockerRunCmd", e.target.value)}
              />
            </FormControl>

            {/* Variables */}
            <FormLabel>Variables</FormLabel>
            {formData.variables.map((v, index) => (
              <HStack key={index}>
                <Input
                  placeholder="Name"
                  value={v.name}
                  onChange={(e) =>
                    handleArrayChange("variables", index, "name", e.target.value)
                  }
                />
                <Select
                  value={v.type}
                  onChange={(e) =>
                    handleArrayChange("variables", index, "type", e.target.value)
                  }
                >
                  <option value="secret">Secret</option>
                  <option value="env">Env</option>
                </Select>
                <IconButton
                  icon={<DeleteIcon />}
                  aria-label="Delete Variable"
                  onClick={() => handleRemoveItem("variables", index)}
                />
              </HStack>
            ))}
            <Button onClick={() => handleAddItem("variables", { name: "", type: "", agents: [] })}>
              Add Variable
            </Button>

            {/* Paths */}
            <FormLabel>Paths</FormLabel>
            {formData.path.map((p, index) => (
              <HStack key={index}>
                <Input
                  placeholder="From"
                  value={p.from}
                  onChange={(e) =>
                    handleArrayChange("path", index, "from", e.target.value)
                  }
                />
                <Input
                  placeholder="To"
                  value={p.to}
                  onChange={(e) =>
                    handleArrayChange("path", index, "to", e.target.value)
                  }
                />
                <IconButton icon={<DeleteIcon />} aria-label="Delete Path" onClick={() => handleRemoveItem("path", index)} />
              </HStack>
            ))}
            <Button onClick={() => handleAddItem("path", { from: "", to: "", type: "", agents: [] })}>
              Add Path
            </Button>

            {/* Ports */}
            <FormLabel>Ports</FormLabel>
            {formData.ports.map((p, index) => (
              <HStack key={index}>
                <Input
                  type="number"
                  placeholder="Container Port"
                  value={p.containerPort}
                  onChange={(e) =>
                    handleArrayChange("ports", index, "containerPort", Number(e.target.value))
                  }
                />
                <IconButton icon={<DeleteIcon />} aria-label="Delete Port" onClick={() => handleRemoveItem("ports", index)} />
              </HStack>
            ))}
            <Button onClick={() => handleAddItem("ports", { containerPort: 0 })}>Add Port</Button>
          </VStack>
        </ModalBody>
        <ModalFooter>
          <Button colorScheme="blue" onClick={handleSubmit}>
            Submit
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
};

export default DeploymentFormModal;
