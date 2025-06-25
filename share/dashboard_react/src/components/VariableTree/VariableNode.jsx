import React from 'react';
import { Box, Text } from '@chakra-ui/react';
import { HiChevronDown, HiChevronRight } from 'react-icons/hi';
import styles from './variableTree.module.scss';

const isLeaf = (val) =>
  typeof val !== 'object' ||
  (Array.isArray(val) && typeof val[0] !== 'object');

const VariableNode = ({ node, path = '', onSelect, toggleExpand, isExpanded }) => {
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
            const expanded = isExpanded(currentPath);

            return (
              <Box key={currentPath}>
                <Box className={styles.nodeHeader}>
                  {isBranch && (
                    <Box
                      as="span"
                      className={styles.expandToggle}
                      onClick={() => toggleExpand(currentPath)}
                    >
                      {expanded ? <HiChevronDown /> : <HiChevronRight />}
                    </Box>
                  )}
                  <Text
                    className={styles.nodeLabel}
                    onClick={() =>
                      !isBranch && onSelect(`{{${currentPath}}}`)
                    }
                  >
                    {key}
                  </Text>
                </Box>
                {isBranch && expanded && (
                  <Box className={styles.nodeGroup}>
                    <VariableNode
                      node={val}
                      path={currentPath}
                      onSelect={onSelect}
                      toggleExpand={toggleExpand}
                      isExpanded={isExpanded}
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
    const expanded = isExpanded(currentPath);

    return (
      <Box className={styles.treeNode}>
        <Box className={styles.nodeHeader}>
          <Box
            as="span"
            className={styles.expandToggle}
            onClick={() => toggleExpand(currentPath)}
          >
            {expanded ? <HiChevronDown /> : <HiChevronRight />}
          </Box>
          <Text
            className={`${styles.nodeLabel} ${styles.wildcard}`}
            onClick={() => onSelect(`{{${currentPath}}}`)}
          >
            #
          </Text>
        </Box>
        {expanded &&
          node.length > 0 &&
          typeof node[0] === 'object' &&
          !Array.isArray(node[0]) && (
            <Box className={styles.nodeGroup}>
              <VariableNode
                node={node[0]}
                path={currentPath}
                onSelect={onSelect}
                toggleExpand={toggleExpand}
                isExpanded={isExpanded}
              />
            </Box>
          )}
      </Box>
    );
  }

  return null;
};

export default React.memo(VariableNode);
