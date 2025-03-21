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
import { HiTrash } from "react-icons/hi";
import RMIconButton from "../../RMIconButton";
import RMButton from "../../RMButton";

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
      if (!v.name) newErrors[`variables[${index}].name`] = "Variable name is required";
    });
  
    formData.path.forEach((p, index) => {
      if (!p.from) newErrors[`path[${index}].from`] = "From path is required";
      if (!p.to) newErrors[`path[${index}].to`] = "To path is required";
    });
  
    formData.ports.forEach((p, index) => {
      if (!p.containerPort || isNaN(p.containerPort) || p.containerPort < 1 || p.containerPort > 65535) {
        newErrors[`ports[${index}].containerPort`] = "Valid port number (1-65535) is required";
      }
    });
  
    formData.gitClones.forEach((g, index) => {
      if (!g.repo) newErrors[`gitClones[${index}].repo`] = "Git repo is required";
    });
  
    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };  

  const handleInputChange = (field, value) => {
    setFormData(prevState  => ({ ...prevState, [field]: value }));
  };

  const handleArrayChange = (field, index, key, value) => {
    setFormData(prevState  => ({
      ...prevState ,
      [field]: prevState[field].map((item, i) =>
        i === index ? { ...item, [key]: value } : item
      ),
    }));
  };

  const handleAddItem = (field, newItem) => {
    setFormData(prevState  => ({ ...prevState, [field]: [...prevState[field], newItem] }));
  };

  const handleRemoveItem = (field, index) => {
    setFormData(prevState  => ({
      ...prevState,
      [field]: prevState[field].filter((_, i) => i !== index),
    }));
  };

  const handleSubmit = () => {
    if (validateForm()) {
      onSubmit(formData);
      onClose();
    }
  };

  return (
    <Modal isOpen={isOpen} onClose={onClose} size={"3xl"}>
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
                {v.type === "secret" ? (
                  <Input
                    type="password"
                    placeholder="Secret"
                    value={v.value}
                    onChange={(e) =>
                      handleArrayChange("variables", index, "value", e.target.value)
                    }
                  />
                ) : (
                  <Input
                    placeholder="Env"
                    value={v.value}
                    onChange={(e) =>
                      handleArrayChange("variables", index, "value", e.target.value)
                    }
                  />
                )}
                <RMIconButton
                  icon={HiTrash}
                  aria-label="Delete Variable"
                  onClick={() => handleRemoveItem("variables", index)}
                />
              </HStack>
            ))}
            <RMButton onClick={() => handleAddItem("variables", { name: "", type: "secret", value: "", agents: [] })}>
              Add Variable
            </RMButton>

            {/* Paths */}
            <FormLabel>Paths</FormLabel>
            {formData.path.map((p, index) => (
              <HStack key={index}>
                <Select
                  value={p.volumedir}
                  onChange={(e) => {
                    handleArrayChange("path", index, "volumedir", e.target.value);
                  }
                  }
                >
                  <option value="etc">etc</option>
                  <option value="log">log</option>
                  <option value="var">var</option>
                </Select>
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
                <RMIconButton icon={HiTrash} aria-label="Delete Path" onClick={() => handleRemoveItem("path", index)} />
              </HStack>
            ))}
            <RMButton onClick={() => handleAddItem("path", { volumedir:"var",from: "", to: "", type: "", agents: [] })}>
              Add Path
            </RMButton>

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
                <RMIconButton icon={HiTrash} aria-label="Delete Port" onClick={() => handleRemoveItem("ports", index)} />
              </HStack>
            ))}
            <RMButton onClick={() => handleAddItem("ports", { containerPort: 0 })}>Add Port</RMButton>

            {/* Git Clone */}
            <FormLabel>Git Clones</FormLabel>
            {formData.gitClones.map((gc, index) => (
              <HStack key={index}>
                <Input
                  placeholder="Repo URL"
                  value={gc.repo}
                  onChange={(e) =>
                    handleArrayChange("gitClones", index, "repo", e.target.value)
                  }
                />
                <Input
                  placeholder="Branch"
                  value={gc.branch}
                  onChange={(e) =>
                    handleArrayChange("gitClones", index, "branch", e.target.value)
                  }
                />
                <Input
                  placeholder="Destination"
                  value={gc.dest}
                  onChange={(e) =>
                    handleArrayChange("gitClones", index, "dest", e.target.value)
                  }
                />
                <Input
                  placeholder="Git User"
                  value={gc.user}
                  onChange={(e) =>
                    handleArrayChange("gitClones", index, "user", e.target.value)
                  }
                />
                <Input
                  type="password"
                  placeholder="Secret"
                  value={gc.pass}
                  onChange={(e) =>
                    handleArrayChange("gitClones", index, "pass", e.target.value)
                  }
                />
                <RMIconButton
                  icon={HiTrash}
                  aria-label="Delete Git Clones"
                  onClick={() => handleRemoveItem("gitClones", index)}
                />
              </HStack>
            ))}
            <RMButton onClick={() => handleAddItem("gitClones", { repo: "", branch: "", dest: "", user: "", pass: "" })}>
              Add Git Clone
            </RMButton>
          </VStack>
        </ModalBody>
        <ModalFooter>
          <RMButton colorScheme="blue" onClick={handleSubmit}>
            Submit
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
};

export default DeploymentFormModal;
