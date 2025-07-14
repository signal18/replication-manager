import React, { useCallback, useEffect, useMemo, useState } from "react"
import { normalizeProps, useMachine } from "@zag-js/react"
import * as tree from "@zag-js/tree-view"
import { useId } from "react"
import { Box, Flex, Modal, ModalBody, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay, Text, VStack } from "@chakra-ui/react"
import { HiChevronRight, HiDocument, HiFolder } from "react-icons/hi"
import { useTheme } from '../../../ThemeProvider'
import CustomIcon from "../../Icons/CustomIcon"
import RMButton from "../../RMButton"
import parentStyles from "../styles.module.scss"

const defaultTree = {
  id: "/",
  name: "/",
  path: "/",
  children: [{
    id: "/",
    name: "/",
    path: "/",
    children: []
  }],
}

const TreeAPIContext = React.createContext(null)

const useTreeAPI = () => {
  const ctx = React.useContext(TreeAPIContext)
  if (!ctx) throw new Error("TreeAPIContext not found")
  return ctx
}

/**
 * getTreeNodeData is a utility function that retrieves the data for a tree node. 
 * It checks if the node is visible based on its state and the parent node's selection status.
 * @param {Object} node - The tree node data.
 * @param {Array} indexPath - The path to the node in the tree, used for indentation.
 * @param {string} parentValue - The value of the parent node, used to determine visibility.
 * @param {boolean} parentSelected - Indicates if the parent node is selected.
 * @param {tree.Api} api - The Zag.js tree-view API for managing node state and interactions.
 */
const getTreeNodeData = (node, indexPath, parentValue, parentSelected, api) => {
  const nodeProps = { indexPath, node }
  const nodeState = api.getNodeState(nodeProps)

  let equal = false, leaf = false, hasPrefix = false, root = false
  
  if (parentSelected) {
    api.selectedValue.forEach(selected => {
      if (!selected) return
      const cleanSelected = selected.trim()
      equal = cleanSelected === nodeState.value.trim() ? true : equal
      hasPrefix = cleanSelected.startsWith(nodeState.value + "/") ? true : hasPrefix
      leaf = cleanSelected === parentValue.trim() ? true : leaf
      root = nodeState.value === "/" ? true : root
    });
  }

  const isVisible = parentSelected ? (equal || hasPrefix || leaf || root) : false

  console.log(`Debug visibility for ${nodeState.value}, parent ${parentValue} => root: ${root}, equal: ${equal}, hasPrefix: ${hasPrefix}, leaf: ${leaf}`)

  if (!isVisible) {
    return {
      nodeProps,
      nodeState,
      isVisible: false,
      isSelected: false,
      getProps: {
        branch: {},
        branchControl: {},
        branchText: {},
        branchContent: {},
        branchIndicator: {},
        branchIndent: {},
        item: {}
      }
    }
  }

  return {
    nodeProps,
    nodeState,
    isVisible,
    isSelected: hasPrefix || equal,
    getProps: {
      branch: api.getBranchProps(nodeProps),
      branchControl: api.getBranchControlProps(nodeProps),
      branchText: api.getBranchTextProps(nodeProps),
      branchContent: api.getBranchContentProps(nodeProps),
      branchIndicator: api.getBranchIndicatorProps(nodeProps),
      branchIndent: api.getBranchIndentGuideProps(nodeProps),
      item: api.getItemProps(nodeProps),
    }
  }
}


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
export const TreeNode = React.memo(({ node, indexPath, parentIndex, nodeState, isSelected, getProps }) => {
  const nodeIndex = indexPath.join("-")
  if (nodeState.isBranch) {
    return (
      <Flex {...getProps.branch} direction="column">
        <Flex {...getProps.branchControl} className={`${parentStyles.treeNode} ${isSelected ? parentStyles.treeNodeSelected : ""}`} direction="row" >
          <CustomIcon icon={HiFolder} />
          <Text {...getProps.branchText}>{node.name}</Text>
          <Box {...getProps.branchIndicator} className={`${parentStyles.folderToggle} ${nodeState.expanded ? parentStyles.folderToggleOpen : ""}`} >
            <CustomIcon icon={HiChevronRight} />
          </Box>
        </Flex>
        <Flex {...getProps.branchContent}>
          <Flex {...getProps.branchIndent} className={parentStyles.indentGuide} direction={"column"}>
            {node.children?.map((childNode, index) => {
              const childIndexPath = [...indexPath, index];
              const childKey = parentIndex ? `tree-${parentIndex}-${index}` : `tree-${index}`;
              return (
                <TreeNodeWrapper key={childKey} node={childNode} indexPath={childIndexPath} parentIndex={nodeIndex} parentSelected={isSelected} parentValue={nodeState.value} />
              );
            })}
          </Flex>
        </Flex>
      </Flex>
    )
  }

  return (
    <Flex {...getProps.item} className={`${parentStyles.treeNode} ${isSelected ? parentStyles.treeNodeSelected : ""}`} direction="row">
      <CustomIcon icon={HiDocument} />
      <Text>{node.name}</Text>
    </Flex>
  )
})

export const TreeNodeWrapper = ({ node, indexPath, parentIndex, parentValue, parentSelected }) => {
  const api = useTreeAPI() // ✅ pulls from context, not props
  const treeNodeData = getTreeNodeData(node, indexPath, parentValue, parentSelected, api)

  if (!treeNodeData.isVisible) return null

  return (
    <TreeNode
      node={node}
      indexPath={indexPath}
      parentIndex={parentIndex}
      nodeState={treeNodeData.nodeState}
      isSelected={treeNodeData.isSelected}
      getProps={treeNodeData.getProps}
    />
  )
}


const TreeViewContent = React.memo(({
  asModal = false,
  title = "Tree View",
  selectedNode = "",
  collection = { rootNode: { children: [] } },
  rootProps,
  labelProps,
  treeProps
}) => {
  return (
    <Flex key="treeview" {...rootProps} direction="column">
      {!asModal && (
        <Text fontWeight="bold" fontSize="lg" mb={2} {...labelProps}>
          {title}
        </Text>
      )}
      <Box key={"selected-box"} mb={4}>
        <Text fontSize="sm" mb={1}>
          Selected Node
        </Text>
        <Box
          key={"input-box"}
          as="input"
          type="text"
          readOnly
          value={selectedNode || ""}
          style={{
            width: "100%",
            padding: "6px",
            borderRadius: "6px",
            border: "1px solid #ccc",
          }}
        />
      </Box>
      <Box key={"tree-box"} {...treeProps}>
        {collection.rootNode.children?.map((node, index) => {
          const indexPath = [index];
          return (
            <TreeNodeWrapper
              key={`tree-node-${node.id || index}`}
              node={node}
              indexPath={indexPath}
              parentIndex={null}
              parentValue={"/"}
              parentSelected={true}
            />
          );
        })}
      </Box>
    </Flex>
  );
});


const TreeView = React.memo(({ title = "Browse Path", treeName="", treeData = defaultTree, nodeToValue = (a) => (a.id), nodeToString = (a) => (a.name), defaultValues = ["/"], asModal = false, isOpen, onClose, onSave }) => {
  const [selectedNode, setSelectedNode] = useState([...defaultValues])
  const { theme } = useTheme()

  useEffect(() => {
    const newValues = []
    defaultValues.forEach((value) => {
      if (value) newValues.push(value)
    })
    setSelectedNode(newValues)
  }, [defaultValues])

  const expandedValues = useMemo(() => {
    const expanded = ["/"]
    selectedNode.forEach((value) => {
      if (value) {
        const parts = value.split("/")
        let currentPath = ""
        parts.forEach((part, index) => {
          if (part) {
            currentPath += `/${part}`
            if (index < parts.length) {
              expanded.push(currentPath)
            }
          }
        })
      }
    })
    return expanded
  },[selectedNode])

  const collection = useMemo(() => tree.collection({
    nodeToValue,
    nodeToString,
    rootNode: treeData,
  }), [treeData, nodeToValue, nodeToString])

  const handleSelect = (node) => {
    console.log("Selected Node:", node)
    let selectedValue = node?.selectedValue || []
    setSelectedNode(selectedValue)
  }

  const handleExpandedChange = (node) => {
    // console.log("Expanded Node:", node)
  }

  const service = useMachine(tree.machine, {
    id: useId(),
    collection,
    
    onSelectionChange: handleSelect,
    selectedValue: selectedNode,
    expandedValue: expandedValues,
  })

  const api = tree.connect(service, normalizeProps)
  const rootProps = api.getRootProps()
  const labelProps = api.getLabelProps()
  const treeProps = api.getTreeProps()

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

  useEffect(() => {
    console.log("selected values", api.selectedValue, "expanded values", api.expandedValue)
  }, [api.selectedValue, api.expandedValue])

  return (
    <TreeAPIContext.Provider value={api}>
      {!asModal ? (
        <TreeViewContent
          key={treeName}
          api={api}
          asModal={asModal}
          title={title}
          selectedNode={selectedNode}
          collection={collection}
          rootProps={rootProps}
          labelProps={labelProps}
          treeProps={treeProps}
        />
      ) : (
        <Modal isOpen={isOpen} onClose={onCloseHandler} size="lg" closeOnOverlayClick={false}>
          <ModalOverlay />
          <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
            <ModalHeader>{title}</ModalHeader>
            <ModalCloseButton />
            <ModalBody>
              <TreeViewContent key={treeName} asModal={asModal} title={title} selectedNode={selectedNode} collection={collection} rootProps={rootProps} labelProps={labelProps} treeProps={treeProps} />
            </ModalBody>
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
      )}
    </TreeAPIContext.Provider>
  )
})

export default TreeView
