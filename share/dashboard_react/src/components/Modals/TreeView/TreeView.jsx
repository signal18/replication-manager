import React, { useEffect, useState } from "react"
import { normalizeProps, useMachine } from "@zag-js/react"
import * as tree from "@zag-js/tree-view"
import { useId } from "react"
import { Box, Flex, Modal, ModalBody, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay, Text } from "@chakra-ui/react"
import { HiChevronRight, HiDocument, HiFolder } from "react-icons/hi"
import { useTheme } from '../../../ThemeProvider'
import CustomIcon from "../../Icons/CustomIcon"
import RMButton from "../../RMButton"
import parentStyles from "../styles.module.scss"

/**
 * TreeNode component renders a single node in the tree view.
 * It handles both branch nodes (folders) and leaf nodes (documents).
 * The component uses the Zag.js tree-view API to manage state and interactions.
 *
 * @param {TreeNodeProps} param0
 * @returns {JSX.Element}
 * @typedef {Object} TreeNodeProps
 * @property {Object} node - The node data containing id, name, and children.
 * @property {Array} indexPath - The path to the node in the tree, used for indentation.
 * @property {tree.Api} api - The Zag.js tree-view API for managing node state and interactions.
 * @description
 */
const TreeNodeComponent = ({ node, indexPath, api, nodeToValue, nodeToString }) => {
  const nodeProps = { indexPath, node }
  const nodeState = api.getNodeState(nodeProps)
  let parentValue = nodeState.value.split("/")
  parentValue.pop() // Remove the current node's value to get the parent value
  if (parentValue.length === 0) {
    parentValue = ["/"]
  } else if (parentValue.length === 1 && parentValue[0] === "") {
    parentValue = ["/"]
  }

    const onBranchClick = () => {
      api.collapse()
      api.select([nodeState.value])
      api.expandParent(nodeState.value)
      api.expand([nodeState.value])
    }

  if (api.selectedValue.length == 0 || api.selectedValue.join(",").trim() == "" || nodeState.value == "/" || api.selectedValue.some((selected) => selected.includes(nodeState.value+"/") || selected === nodeState.value) || api.selectedValue.some((selected) => selected == parentValue.join("/"))) {
    if (nodeState.isBranch) {
      return (
        <Flex {...api.getBranchProps(nodeProps)} direction="column">
          <Flex {...{...api.getBranchControlProps(nodeProps), onClick: onBranchClick}} className={`${parentStyles.treeNode} ${api.selectedValue.includes(nodeState.value) ? parentStyles.treeNodeSelected : ""}`} direction="row" >
            <CustomIcon icon={HiFolder} />
            <Text {...api.getBranchTextProps(nodeProps)}>{node.name}</Text>
            <Box {...api.getBranchIndicatorProps(nodeProps)} className={`${parentStyles.folderToggle} ${nodeState.expanded ? parentStyles.folderToggleOpen : ""}`} >
              <CustomIcon icon={HiChevronRight} />
            </Box>
          </Flex>
          <Flex {...api.getBranchContentProps(nodeProps)}>
            <Flex {...api.getBranchIndentGuideProps(nodeProps)} className={parentStyles.indentGuide} direction={"column"}>
              {node.children?.map((childNode, index) => (
                <TreeNode
                  key={childNode.id}
                  node={childNode}
                  indexPath={[...indexPath, index]}
                  api={api}
                />
              ))}
            </Flex>
          </Flex>
        </Flex>
      )
    }

    return (
      <Flex {...api.getItemProps(nodeProps)} className={`${parentStyles.treeNode} ${api.selectedValue.includes(nodeState.value) ? parentStyles.treeNodeSelected : ""}`} direction="row">
        <CustomIcon icon={HiDocument} />
        <Text>{node.name}</Text>
      </Flex>
    )
  }

  return (<></>)
}

export const TreeNode = React.memo(TreeNodeComponent)

const TreeView = React.memo(({ title, treeData, nodeToValue, nodeToString, defaultValues = ["/"], asModal = false, modalTitle = "Browse Path", isOpen, onClose, onSave }) => {
  const [selectedNode, setSelectedNode] = useState([...defaultValues])
  const { theme } = useTheme()

  const collection = tree.collection({
    nodeToValue: nodeToValue || ((node) => node.id),
    nodeToString: nodeToString || ((node) => node.name),
    rootNode: treeData,
  })

  const handleSelect = (node) => {
    let selectedValue = node?.selectedValue || []
    if (Array.isArray(selectedValue)) {
      selectedValue = selectedValue.map((item) => item.id || item)
    }
    setSelectedNode(selectedValue)
    console.log("Selected Node:", selectedValue)
  }

  const handleExpandedChange = (node) => {
    console.log("Expanded Node:", node)
  }

  const service = useMachine(tree.machine, {
    id: useId(),
    collection,
    onSelectionChange: handleSelect,
    onExpandedChange: handleExpandedChange,
    defaultSelectedValue: defaultValues,
  })

  const api = tree.connect(service, normalizeProps)

  const handleSave = () => {
    if (selectedNode.length > 0) {
      if (onSave) onSave(selectedNode.join(", "))
      setSelectedNode(null)
      if (asModal) onClose()
    }
  }
  const onCloseHandler = () => {
    setSelectedNode(null)
    if (onClose) onClose()
  }

  const content = (
    <Box {...api.getRootProps()}>
      <Text fontWeight="bold" fontSize="lg" mb={2} {...api.getLabelProps()}>
        {title}
      </Text>
      <Box mb={4}>
        <Text fontSize="sm" mb={1}>Selected Node</Text>
        <Box as="input" type="text" readOnly value={selectedNode || ""} style={{ width: "100%", padding: "6px", borderRadius: "6px", border: "1px solid #ccc" }} />
      </Box>
      <Box {...api.getTreeProps()}>
        {collection.rootNode.children?.map((node, index) => (
          <TreeNode key={node.id} node={node} indexPath={[index]} api={api} nodeToValue={nodeToValue} nodeToString={nodeToString} />
        ))}
      </Box>
    </Box>
  )

  if (!asModal) return content

  return (
    <Modal isOpen={isOpen} onClose={onCloseHandler} size="lg" closeOnOverlayClick={false}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{modalTitle}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>{content}</ModalBody>
        <ModalFooter>
          <Box display="flex" justifyContent="flex-end" width="100%">
            <RMButton onClick={onCloseHandler} variant="outline" mr={3}>
              Cancel
            </RMButton>
            <RMButton onClick={handleSave} disabled={!selectedNode}>
              Save
            </RMButton>
          </Box>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
})

export default TreeView
