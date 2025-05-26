import { useDispatch } from 'react-redux'
import { useState } from 'react'
import { FormControl, FormLabel, HStack, Input, Select, VStack } from '@chakra-ui/react'
import TextForm from '../../../../components/TextForm'
import Dropdown from '../../../../components/Dropdown'
import RMIconButton from '../../../../components/RMIconButton'
import { HiTrash } from 'react-icons/hi'
import RMButton from '../../../../components/RMButton'
import { deploymentFieldChange, deploymentFieldIndexAdd, deploymentFieldIndexDrop } from '../../../../redux/clusterSlice'

const initialState = {
  name: "",
  variables: [],
  path: [],
  ports: [],
  dockerImg: "",
  dockerRunArgs: "",
  dockerRunCmd: "",
  gitClones: [],
}

const defaultConfirmText = "Are you sure you want to change this field to: ";

function DeploymentDetail({ clusterName, appId, row, deployId }) {
  const dispatch = useDispatch()
  const [formData, setFormData] = useState(initialState)

  const variableTypes = [
    { value: 'secret', name: 'Secret' },
    { value: 'env', name: 'Env' },
  ]

  const volumeDirs = [
    { value: 'etc', name: 'etc' },
    { value: 'log', name: 'log' },
    { value: 'var', name: 'var' },
  ]

  const handleInputChange = (field, value) => {
    dispatch(deploymentFieldChange({ clusterName, appId, deployId, field, value }))
  };

  const handleSaveArrayChange = (field, index, key, value) => {
    dispatch(deploymentFieldChange({ clusterName, appId, deployId, field, index, key, value }))
  };

  const handleSaveAddItem = (field, value) => {
    dispatch(deploymentFieldIndexAdd({ clusterName, appId, deployId, field, value })).then(() => {
      // Reset the form data for the field after saving
      setFormData(prevState => ({
        ...prevState,
        [field]: initialState[field]
      }))
    });
  }

  const handleDropIndex = (field, index) => {
    dispatch(deploymentFieldIndexDrop({ clusterName, appId, deployId, field, index }))
  };

  const handleArrayChange = (field, index, key, value) => {
    if (key === null) {
      setFormData(prevState => ({
        ...prevState,
        [field]: prevState[field].map((item, i) =>
          i === index ? value : item
        ),
      }));
    } else {
      setFormData(prevState => ({
        ...prevState,
        [field]: prevState[field].map((item, i) =>
          i === index ? { ...item, [key]: value } : item
        ),
      }));
    }
  };

  const handleAddItem = (field, newItem) => {
    setFormData(prevState => ({ ...prevState, [field]: [...prevState[field], newItem] }));
  };

  const handleRemoveItem = (field, index) => {
    setFormData(prevState => ({
      ...prevState,
      [field]: prevState[field].filter((_, i) => i !== index),
    }));
  };

  return (
    <VStack spacing={4} align="stretch">
      <FormControl>
        <FormLabel>Name</FormLabel>
        <TextForm confirmTitle={defaultConfirmText} isDisabled={true} name="name" value={row.name} onSave={(value) => handleInputChange("name", value)} />
      </FormControl>

      <FormControl>
        <FormLabel>Docker Image</FormLabel>
        <TextForm confirmTitle={defaultConfirmText} name="dockerImg" value={row.dockerImg} onSave={(value) => handleInputChange("dockerImg", value)}
        />
      </FormControl>

      <FormControl>
        <FormLabel>Docker Run Cmd</FormLabel>
        <TextForm confirmTitle={defaultConfirmText} name="dockerRunCmd" value={row.dockerRunCmd} onSave={(value) => handleInputChange("dockerRunCmd", value)} />
      </FormControl>

      {/* Variables */}
      <FormLabel>Variables</FormLabel>
      {row.variables.map((v, index) => (
        <HStack key={index}>
          <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].name`} placeholder="Name" value={v.name} onSave={(value) => handleSaveArrayChange("variables", index, "name", value)} />
          <Dropdown id={`variables[${index}].type`} confirmTitle={"Are you sure to change variable type: "} selectedValue={v.type} onChange={(e) => handleSaveArrayChange("variables", index, "type", e.target.value)} options={variableTypes} />
          {v.type === "secret" ? (
            <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].secret`} type="password" placeholder="Secret" value={v.value} onSave={(value) => handleSaveArrayChange("variables", index, "value", value)} />
          ) : (
            <TextForm confirmTitle={defaultConfirmText} name={`variables[${index}].env`} placeholder="Env" value={v.value} onSave={(value) => handleSaveArrayChange("variables", index, "value", value)} />
          )}
          <RMIconButton icon={HiTrash} aria-label="Delete Variable" onClick={() => handleDropIndex("variables", index)} />
        </HStack>
      ))}
      {formData.variables.map((v, index) => (
        <HStack key={index}>
          <Input name={`variables[${index}].name`} placeholder="Name" value={v.name} onChange={(e) => handleArrayChange("variables", index, "name", e.target.value)} />
          <Select value={v.type} onChange={(e) => handleArrayChange("variables", index, "type", e.target.value)}>
            <option value="secret">Secret</option>
            <option value="env">Env</option>
          </Select>
          {v.type === "secret" ? (
            <Input name={`variables[${index}].secret`} type="password" placeholder="Secret" value={v.value} onChange={(e) => handleArrayChange("variables", index, "value", e.target.value)} />
          ) : (
            <Input name={`variables[${index}].env`} placeholder="Env" value={v.value} onChange={(e) => handleArrayChange("variables", index, "value", e.target.value)} />
          )}
          <RMIconButton icon={HiTrash} aria-label="Delete Variable" onClick={() => handleRemoveItem("variables", index)}
          />
        </HStack>
      ))}
      <HStack>
        {formData.variables?.length > 0 && (
          <RMButton onClick={() => handleSaveAddItem("variables", formData.variables)}>
            Save Variables
          </RMButton>
        )}
        <RMButton onClick={() => handleAddItem("variables", { name: "", type: "secret", value: "", agents: [] })}>
          Add Variable
        </RMButton>
      </HStack>

      {/* Paths */}
      <FormLabel>Paths</FormLabel>
      {row.path.map((p, index) => (
        <HStack key={index}>
          <Dropdown confirmTitle={"Are you sure to change volumedir: "} selectedValue={p.volumedir} onChange={(value) => handleSaveArrayChange("path", index, "volumedir", value)} options={volumeDirs} />
          <TextForm confirmTitle={defaultConfirmText} name={`path[${index}].from`} placeholder="From" value={p.from} onSave={(value) => handleSaveArrayChange("path", index, "from", value)} />
          <TextForm confirmTitle={defaultConfirmText} name={`path[${index}].to`} placeholder="To" value={p.to} onSave={(value) => handleSaveArrayChange("path", index, "to", value)} />
          <RMIconButton icon={HiTrash} aria-label="Delete Path" onClick={() => handleDropIndex("path", index)} />
        </HStack>
      ))}
      {formData.path.map((p, index) => (
        <HStack key={index}>
          <Select value={p.volumedir} onChange={(e) => handleArrayChange("path", index, "volumedir", e.target.value)} >
            <option value="etc">etc</option>
            <option value="log">log</option>
            <option value="var">var</option>
          </Select>
          <Input name={`path[${index}].from`} placeholder="From" value={p.from} onChange={(e) => handleArrayChange("path", index, "from", e.target.value)} />
          <Input name={`path[${index}].to`} placeholder="To" value={p.to} onChange={(e) => handleArrayChange("path", index, "to", e.target.value)} />
          <RMIconButton icon={HiTrash} aria-label="Delete Path" onClick={() => handleRemoveItem("path", index)} />
        </HStack>
      ))}
      <HStack>
        {formData.path?.length > 0 && (
          <RMButton onClick={() => handleSaveAddItem("path", formData.path)}>
            Save Path
          </RMButton>
        )}
        <RMButton onClick={() => handleAddItem("path", { volumedir: "var", from: "", to: "", type: "", agents: [] })}>
          Add Path
        </RMButton>
      </HStack>

      {/* Ports */}
      <FormLabel>Ports</FormLabel>
      {row.ports.map((p, index) => (
        <HStack key={index}>
          <TextForm confirmTitle={defaultConfirmText} pattern="^[0-9]{1,5}(:[0-9]{1,5})?$" placeholder="Container Port" value={p} onSave={(value) => handleSaveArrayChange("ports", index, null, value)} />
          <RMIconButton icon={HiTrash} aria-label="Delete Port" onClick={() => handleDropIndex("ports", index)} />
        </HStack>
      ))}
      {formData.ports.map((p, index) => (
        <HStack key={index}>
          <Input pattern="^[0-9]{1,5}(:[0-9]{1,5})?$" placeholder="Container Port" value={p} onChange={(e) => handleArrayChange("ports", index, null, e.target.value)} />
          <RMIconButton icon={HiTrash} aria-label="Delete Port" onClick={() => handleRemoveItem("ports", index)} />
        </HStack>
      ))}
      <HStack>
        {formData.ports?.length > 0 && (
          <RMButton onClick={() => handleSaveAddItem("ports", formData.ports)}>
            Save Port
          </RMButton>
        )}
        <RMButton onClick={() => handleAddItem("ports", "")}>
          Add Port
        </RMButton>
      </HStack>

      {/* Git Clone */}
      <FormLabel>Git Clones</FormLabel>
      {row.gitClones.map((gc, index) => (
        <HStack key={index}>
          <TextForm confirmTitle={defaultConfirmText} name={`gitClones[${index}].repo`} placeholder="Repo URL" value={gc.repo} onSave={(value) => handleSaveArrayChange("gitClones", index, "repo", value)} />
          <TextForm confirmTitle={defaultConfirmText} name={`gitClones[${index}].branch`} placeholder="Branch" value={gc.branch} onSave={(value) => handleSaveArrayChange("gitClones", index, "branch", value)} />
          <TextForm confirmTitle={defaultConfirmText} name={`gitClones[${index}].dest`} placeholder="Destination" value={gc.dest} onSave={(value) => handleSaveArrayChange("gitClones", index, "dest", value)} />
          <TextForm confirmTitle={defaultConfirmText} name={`gitClones[${index}].user`} placeholder="Git User" value={gc.user} onSave={(value) => handleSaveArrayChange("gitClones", index, "user", value)} />
          <TextForm confirmTitle={defaultConfirmText} name={`gitClones[${index}].pass`} type="password" placeholder="Secret" value={gc.pass} onSave={(value) => handleSaveArrayChange("gitClones", index, "pass", value)} />
          <RMIconButton
            icon={HiTrash}
            aria-label="Delete Git Clones"
            onClick={() => handleDropIndex("gitClones", index)}
          />
        </HStack>
      ))}
      {formData.gitClones.map((gc, index) => (
        <HStack key={index}>
          <Input name={`gitClones[${index}].repo`} placeholder="Repo URL" value={gc.repo} onChange={(e) => handleArrayChange("gitClones", index, "repo", e.target.value)} />
          <Input name={`gitClones[${index}].branch`} placeholder="Branch" value={gc.branch} onChange={(e) => handleArrayChange("gitClones", index, "branch", e.target.value)} />
          <Input name={`gitClones[${index}].dest`} placeholder="Destination" value={gc.dest} onChange={(e) => handleArrayChange("gitClones", index, "dest", e.target.value)} />
          <Input name={`gitClones[${index}].user`} placeholder="Git User" value={gc.user} onChange={(e) => handleArrayChange("gitClones", index, "user", e.target.value)} />
          <Input name={`gitClones[${index}].pass`} type="password" placeholder="Secret" value={gc.pass} onChange={(e) => handleArrayChange("gitClones", index, "pass", e.target.value)} />
          <RMIconButton
            icon={HiTrash}
            aria-label="Delete Git Clones"
            onClick={() => handleRemoveItem("gitClones", index)}
          />
        </HStack>
      ))}
      <HStack>
        {formData.gitClones?.length > 0 && (
          <RMButton onClick={() => handleSaveAddItem("gitClones", formData.gitClones)}>
            Save Git Clone
          </RMButton>
        )}
        <RMButton onClick={() => handleAddItem("gitClones", { repo: "", branch: "", dest: "", user: "", pass: "" })}>
          Add Git Clone
        </RMButton>
      </HStack>
    </VStack>
  )
}

export default DeploymentDetail
