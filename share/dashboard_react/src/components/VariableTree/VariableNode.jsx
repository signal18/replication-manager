import React from 'react';
import { Box, Text } from '@chakra-ui/react';
import styles from './variableTree.module.scss';

const VariableNode = ({ node, path = '', onSelect }) => {
  if (node === null || node === undefined) {
    return null; // Handle null or undefined nodes
  }

  const buildPath = (key) => (path ? `${path}.${key}` : key);

  // Handle object
  if (typeof node === 'object' && !Array.isArray(node)) {
    return (
      <Box className={styles.treeNode}>
        {Object.entries(node)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([key, val]) => {
            const currentPath = buildPath(key);
            const clickable = (typeof val !== 'object') || (Array.isArray(val) && typeof val[0] !== 'object');
            return (
              <Box key={currentPath}>
                <Text
                  className={styles.nodeLabel}
                  onClick={() =>
                    clickable && onSelect(`{{${currentPath}}}`)
                  }
                >
                  {key}
                </Text>
                {typeof val === 'object' && (
                  <Box className={styles.nodeGroup}>
                    <VariableNode
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
    const currentPath = buildPath("#");
    return (
      <Box className={styles.treeNode}>
        {/* Clickable wildcard # */}
        <Text
          className={`${styles.nodeLabel} ${styles.wildcard}`}
          onClick={() => onSelect(`{{${currentPath}}}`)}
        >
          #
        </Text>

        {/* Handle #.key from first object if structured */}
        {node.length > 0 &&
          typeof node[0] === 'object' && (
            <Box className={styles.nodeGroup}>
              <VariableNode
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
