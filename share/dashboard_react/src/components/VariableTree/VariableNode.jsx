import React, { useState } from 'react';
import { Box, HStack, Text } from '@chakra-ui/react';
import { HiChevronDown, HiChevronRight } from 'react-icons/hi';
import styles from './variableTree.module.scss';

const isLeaf = (val) =>
  typeof val !== 'object' ||
  (Array.isArray(val) && typeof val[0] !== 'object');

const VariableNode = ({ node, path = '', onSelect, autoExpand = false }) => {
  const [isOpen, setIsOpen] = useState(autoExpand);

  if (node === null || node === undefined) return null;

  const buildPath = (key) => (path ? `${path}.${key}` : key);

  // Handle object
  if (typeof node === 'object' && !Array.isArray(node)) {
    return (
      <Box className={styles.treeNode}>
        {Object.entries(node)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([key, val]) => {
            const currentPath = buildPath(key);
            const isBranch = !isLeaf(val);

            return (
              <Box key={currentPath}>
                <HStack gap={2} className={styles.nodeHeader}>
                  <Text
                    className={styles.nodeLabel}
                    onClick={() => isBranch ? setIsOpen(!isOpen) : onSelect(`{{${currentPath}}}`) }
                  >
                    {key}
                  </Text>
                  {isBranch && (
                    <Box
                      as="span"
                      className={styles.expandToggle}
                      onClick={() => setIsOpen(!isOpen)}
                    >
                      {isOpen ? <HiChevronDown /> : <HiChevronRight />}
                    </Box>
                  )}
                </HStack>
                {isBranch && isOpen && (
                  <Box className={styles.nodeGroup}>
                    <VariableNode
                      key={currentPath}
                      node={val}
                      path={currentPath}
                      onSelect={onSelect}
                    />
                  </Box>
                )}
              </Box>
            );
          })}
      </Box>
    );
  }

  // Handle array
  if (Array.isArray(node)) {
    const currentPath = buildPath('#');

    return (
      <Box className={styles.treeNode}>
        <HStack gap={2} className={styles.nodeHeader}>
          <Text
            className={`${styles.nodeLabel} ${styles.wildcard}`}
            onClick={() => onSelect(`{{${currentPath}}}`)}
          >
            #
          </Text>
          <Box
            as="span"
            className={styles.expandToggle}
            onClick={() => setIsOpen(!isOpen)}
          >
            {isOpen ? <HiChevronDown /> : <HiChevronRight />}
          </Box>
        </HStack>
        {isOpen &&
          node.length > 0 &&
          typeof node[0] === 'object' &&
          !Array.isArray(node[0]) && (
            <Box className={styles.nodeGroup}>
              <VariableNode
                key={currentPath}
                node={node[0]}
                path={currentPath}
                onSelect={onSelect}
              />
            </Box>
          )}
      </Box>
    );
  }

  return null;
};

export default VariableNode;
