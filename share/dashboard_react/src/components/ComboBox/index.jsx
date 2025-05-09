import {
    Box,
    Input,
    List,
    ListIcon,
    ListItem,
    Popover,
    PopoverContent,
    PopoverTrigger,
    useDisclosure,
    IconButton,
    VStack,
    HStack,
} from "@chakra-ui/react";
import styles from "./styles.module.scss";
import { useState, useEffect, useRef } from "react";
import { FaCheck, FaCheckCircle, FaTimes } from "react-icons/fa";

const ComboBox = ({
    options = [],
    placeholder = "Select or type...",
    inputClassName,
    boxClassName,
    popoverClassName,
    onConfirm,
    caseSensitive = false,
    clearAfterConfirm = false,
    listMaxHeight = "200px",
    isDisabled = false,
}) => {
    const [inputValue, setInputValue] = useState("");
    const [filteredOptions, setFilteredOptions] = useState(options);
    const { isOpen, onOpen, onClose } = useDisclosure();
    const inputRef = useRef();


    useEffect(() => {
        setFilteredOptions(options);
    }, [options]);

    const handleInputChange = (e) => {
        if (isDisabled) return; // Prevent input when disabled

        const val = e.target.value;
        setInputValue(val);

        // Restore focus just in case
        setTimeout(() => {
            inputRef.current?.focus();
        }, 0);

        // Filter options based on case sensitivity
        const filtered = options.filter((opt) =>
            caseSensitive ? opt.includes(val) : opt.toLowerCase().includes(val.toLowerCase())
        );
        setFilteredOptions(filtered);

        if (val.length > 0 || filtered.length > 0) {
            onOpen();
        } else {
            onClose();
        }
    };

    const handleSelect = (value) => {
        setInputValue(value);
        onClose();
    };

    const handleConfirm = () => {
        if (onConfirm) {
            onConfirm(inputValue);
        }
        if (clearAfterConfirm) {
            setInputValue(""); // Clear the input after confirming
        }
        onClose();
    };

    const handleCancel = () => {
        setInputValue(""); // Clear the input field
        onClose(); // Close the popover
    };

    return (
        <Box className={boxClassName}>
            <HStack spacing={2} >
                <Popover isOpen={isOpen} onClose={onClose} placement="bottom-start">
                    <PopoverTrigger>
                        <Input
                            flex="1"
                            ref={inputRef}
                            placeholder={placeholder}
                            className={inputClassName}
                            value={inputValue}
                            onChange={handleInputChange}
                            onFocus={(e) => { if (!isDisabled) { onOpen(e) }}}
                            isDisabled={isDisabled}
                        />
                    </PopoverTrigger>
                    <PopoverContent className={popoverClassName}>
                        <VStack align="stretch" spacing={2} p={2}>
                            <List spacing={1} maxH={listMaxHeight} overflowY="auto" className={styles.list}>
                                {filteredOptions.length > 0 ? (
                                    filteredOptions.map((option) => (
                                        <ListItem
                                            key={option}
                                            p={2}
                                            borderRadius="md"
                                            cursor="pointer"
                                            onClick={() => handleSelect(option)}
                                        >
                                            <ListIcon as={FaCheckCircle} color="green.500" />
                                            {option}
                                        </ListItem>
                                    ))
                                ) : (
                                    <ListItem p={2} color="gray.500">
                                        No options
                                    </ListItem>
                                )}
                            </List>
                        </VStack>
                    </PopoverContent>
                </Popover>
                {inputValue.length > 0 && (<>
                    <IconButton
                        icon={<FaCheck />}
                        aria-label="Confirm"
                        colorScheme="blue"
                        onClick={handleConfirm}
                        alignSelf="flex-end"
                        size="sm"
                        title="Confirm input"
                        isDisabled={isDisabled}
                    />
                    <IconButton
                        icon={<FaTimes />}
                        aria-label="Cancel"
                        colorScheme="red"
                        onClick={handleCancel}
                        alignSelf="flex-end"
                        size="sm"
                        title="Cancel input"
                        isDisabled={isDisabled}
                    />
                </>)}
            </HStack>
        </Box>
    );
};

export default ComboBox;
