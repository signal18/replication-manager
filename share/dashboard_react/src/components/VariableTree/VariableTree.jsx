import React, { useState } from 'react';
import VariableNode from './VariableNode';
import styles from './variableTree.module.scss';

const VariableTree = ({ variables, onSelect }) => {
  const [expandedPaths, setExpandedPaths] = useState(new Set());

  const toggleExpand = (path) => {
    setExpandedPaths(prev => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  };

  const isExpanded = (path) => expandedPaths.has(path);

  return (
    <div className={styles.variableTree}>
      {variables && (<VariableNode key={"root"} node={variables} onSelect={onSelect} path="" toggleExpand={toggleExpand} isExpanded={isExpanded} />)}
    </div>
  );
};

export default React.memo(VariableTree);
