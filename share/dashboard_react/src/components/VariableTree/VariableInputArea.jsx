import { useEffect, useRef, useState } from 'react';
import { Box, Input, Textarea } from '@chakra-ui/react';
import {
  HiPencilAlt,
  HiCheck,
  HiX,
  HiEye,
  HiEyeOff,
} from 'react-icons/hi';
import VariableTree from './VariableTree';
import styles from './variableInputArea.module.scss';
import treeStyles from './variableTree.module.scss';
import RMIconButton from '../RMIconButton';
import ConfirmModal from '../Modals/ConfirmModal';
import { debounce } from 'lodash';

const VariableInputArea = ({
  value,
  onChange = () => { },
  onSave = () => { },
  variables,
  placeholder = 'Enter value...',
  multiline = false,
  rows = 4,
  isDisabled = false,
  type = 'text',
  useConfirmModal = false,
  confirmTitle = 'Save confirmation',
  confirmBody = 'Are you sure you want to save this value? This action cannot be undone.',
  alwaysEditable = false,
  className
}) => {
  const inputRef = useRef(null);
  const [currentValue, setCurrentValue] = useState(value);
  const [previousValue, setPreviousValue] = useState(value);
  const [isEditable, setIsEditable] = useState(alwaysEditable ? true : false);
  const [isOpen, setIsOpen] = useState(false); // for password toggle
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false);

  const isPassword = type === 'password';
  const valid = currentValue !== '';
  const editable = alwaysEditable || isEditable;

  useEffect(() => {
    setCurrentValue(value);
    setPreviousValue(value);
  }, [value]);

  const debouncedOnChange = debounce((newValue) => {
    onChange(newValue);
  }, 300);

  useEffect(() => {
    return () => {
      debouncedOnChange.cancel();
    };
  }, []);  

  const handleChange = (value) => {
    if (value === currentValue) return;
    setCurrentValue(value);
    if (alwaysEditable) debouncedOnChange(value);
  };

  const handleInsert = (variable) => {
    if (isDisabled || !editable) return;

    const input = inputRef.current;
    const start = input.selectionStart;
    const end = input.selectionEnd;

    const before = currentValue.substring(0, start);
    const after = currentValue.substring(end);
    const newValue = before + variable + after;

    handleChange(newValue);

    setTimeout(() => {
      input.focus();
      input.setSelectionRange(start + variable.length, start + variable.length);
    }, 0);
  };

  const handleConfirmSave = () => {
    onSave(currentValue);
    setIsConfirmModalOpen(false);
    setIsEditable(false);
  };

  const handleConfirmCancel = () => {
    setIsConfirmModalOpen(false);
  };

  const handleSave = () => {
    setPreviousValue(currentValue);
    if (useConfirmModal) {
      setIsConfirmModalOpen(true);
    } else {
      onSave(currentValue);
      setIsEditable(false);
    }
  };

  const handleCancel = () => {
    setCurrentValue(previousValue);
    setIsEditable(false);
  };

  useEffect(() => {
    if (isConfirmModalOpen) {
      inputRef.current?.blur();
    } else if (!alwaysEditable) {
      inputRef.current?.focus();
    }
  }, [isConfirmModalOpen]);

  return (
    <Box className={`${styles.variableInputArea} ${className}`}>
      <Box className={styles.inputWrapper}>
        {multiline ? (
          <Textarea
            ref={inputRef}
            className={styles.inputField}
            rows={rows}
            value={currentValue}
            onChange={(e) => handleChange(e.target.value)}
            onBlur={() => { if (alwaysEditable) debouncedOnChange(currentValue) }}
            placeholder={placeholder}
            isDisabled={isDisabled}
            isReadOnly={!editable && !isDisabled}
          />
        ) : (
          <Input
            ref={inputRef}
            className={styles.inputField}
            type={isPassword && !isOpen ? 'password' : 'text'}
            value={currentValue}
            onChange={(e) => handleChange(e.target.value)}
            onBlur={() => { if (alwaysEditable) debouncedOnChange(currentValue) }}
            placeholder={placeholder}
            isDisabled={isDisabled}
            isReadOnly={!editable && !isDisabled}
          />
        )}

        {!alwaysEditable && (
          <Box className={styles.buttonGroup}>
            {isEditable ? (
              <>
                {isPassword && (
                  <RMIconButton
                    isDisabled={isDisabled}
                    icon={isOpen ? HiEyeOff : HiEye}
                    aria-label={isOpen ? 'Mask password' : 'Reveal password'}
                    onClick={() => setIsOpen(!isOpen)}
                  />
                )}
                <RMIconButton
                  isDisabled={isDisabled}
                  icon={HiX}
                  tooltip="Cancel"
                  colorScheme="red"
                  onClick={handleCancel}
                />
                <RMIconButton
                  icon={HiCheck}
                  colorScheme="green"
                  isDisabled={isDisabled || !valid || currentValue === previousValue}
                  tooltip="Save"
                  onClick={handleSave}
                />
              </>
            ) : (
              <RMIconButton
                isDisabled={isDisabled}
                icon={HiPencilAlt}
                className={styles.btnEdit}
                tooltip="Edit"
                onClick={() => {
                  setIsEditable(true);
                  setTimeout(() => inputRef.current?.focus(), 0);
                }}
              />
            )}
          </Box>
        )}
      </Box>

      {editable && !isDisabled && (
        <Box className={treeStyles.variableTree}>
          <VariableTree variables={variables} onSelect={handleInsert} />
        </Box>
      )}

      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          onConfirmClick={handleConfirmSave}
          closeModal={handleConfirmCancel}
          title={confirmTitle}
          body={confirmBody}
        />
      )}
    </Box>
  );
};

export default VariableInputArea;
