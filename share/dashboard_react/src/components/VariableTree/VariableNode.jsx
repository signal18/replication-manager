import React from 'react';
import { Box, Text } from '@chakra-ui/react';
import styles from './variableTree.module.scss';

const VariableNode = ({ node, path, onSelect }) => {
  if (typeof node === 'object' && !Array.isArray(node)) {
    return (
      <Box className={styles.treeNode}>
        {Object.entries(node).map(([key, val]) => (
          <Box key={key}>
            <Text
              className={styles.nodeLabel}
              onClick={() =>
                typeof val !== 'object' && onSelect(`{{${path ? `${path}.` : ''}${key}}}`)
              }
            >
              {key}
            </Text>
            {typeof val === 'object' && (
              <Box className={styles.nodeGroup}>
                <VariableNode
                  node={val}
                  path={`${path ? `${path}.` : ''}${key}`}
                  onSelect={onSelect}
                />
              </Box>
            )}
          </Box>
        ))}
      </Box>
    );
  }

  if (Array.isArray(node)) {
    return (
      <Box className={styles.treeNode}>
        <Text
          className={`${styles.nodeLabel} ${styles.wildcard}`}
          onClick={() => onSelect(`{{${path}.#}}`)}
        >
          #
        </Text>
        {node.length > 0 &&
          typeof node[0] === 'object' &&
          Object.keys(node[0]).map((key) => (
            <Box key={key} className={styles.nodeGroup}>
              <Text
                className={styles.nodeLabel}
                onClick={() => onSelect(`{{${path}.#.${key}}}`)}
              >
                #.{key}
              </Text>
            </Box>
          ))}
      </Box>
    );
  }

  return null;
};

export default VariableNode;
