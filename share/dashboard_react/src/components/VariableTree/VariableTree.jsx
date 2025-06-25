import React from 'react';
import VariableNode from './VariableNode';
import styles from './variableTree.module.scss';

const VariableTree = ({ variables, onSelect }) => {
  return (
    <div className={styles.variableTree}>
      <VariableNode node={variables} onSelect={onSelect} path="" />
    </div>
  );
};

export default VariableTree;
