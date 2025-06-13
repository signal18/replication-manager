import { Box, Checkbox, Flex, Text } from "@chakra-ui/react";
import React, { useEffect, useMemo, useState } from "react";
import styles from "./styles.module.scss"
import ConfirmModal from "../Modals/ConfirmModal";
import { useDispatch } from "react-redux";
import { showWarningToast } from "../../redux/toastSlice";

/**
 * Checkboxes component for rendering a list of checkboxes with optional confirmation modal.
 * @param {Object} props 
 * @returns 
 */
const Checkboxes = ({ list = [], values = [], onChange = () => { }, parentStyles = {}, confirm = false, confirmTitle = "Change value to: ", splitConfirm = false, direction = "column" }) => {
    const [selected, setSelected] = useState([])
    const [oldSelected, setOldSelected] = useState([])
    const [isOpen, setIsOpen] = useState(false)

    const dispatch = useDispatch()

    const options = useMemo(() => {
        if (!Array.isArray(list)) {
            if (list?.length === 0) {
                return [];
            }
            return list.split(",").map(item => ({
                name: item.trim(),
                value: item.trim()
            }));
        }
        return list.map(item => ({
            name: item.name || item.label || item,
            value: item.value || item.name || item
        }));
    }, [list]);


    useEffect(() => {
        let newval = []
        if (values) {
            newval = Array.isArray(values)
                ? values
                : values.split(",").map(v => v.trim());
        }

        const isSame = JSON.stringify(newval) === JSON.stringify(selected);

        if (!isOpen || isSame) {
            setSelected(newval);
            setOldSelected(newval);
        } else {
            dispatch(showWarningToast({title: "Data has changed", description: "The data has been changed externally while modal opened"}))
        }
    }, [values]);



    const allChecked = options.length > 0 && options.every(option => selected.includes(option.value));
    const indeterminate = options.length > 0 && options.some(option => selected.includes(option.value)) && !allChecked;

    const handleChange = (value) => {
        const updated = selected.includes(value)
            ? selected.filter(item => item !== value)
            : [...selected, value];
        setSelected(updated);
        if (confirm) setIsOpen(true);
    }

    const handleAllChange = (checked) => {
        const updated = checked ? [] : options.map(item => item.value);
        setSelected(updated);
        if (confirm) setIsOpen(true);
    }


    const confirmBody = splitConfirm ? (
        <Flex direction={"column"}>
            <Text>{`Change value to:`}</Text>
            {selected.map((item) => <Text key={item}>{item}</Text>)}
        </Flex>
    ) : (
        <Flex direction={"column"}>
            <Text>Change value to: {selected.join(", ")}</Text>
        </Flex>
    )

    return (
        <>
            <Flex
                className={`${styles.List} ${parentStyles?.List}`}
                direction={direction === "row" ? "row" : "column"}
                wrap="wrap"
                gap={2}
            >
                {options.length > 0 ? (
                    <>
                        <Box className={`${styles.ListItem} ${parentStyles?.ListItem}`}>
                            <Checkbox isChecked={allChecked} isIndeterminate={indeterminate} onChange={(e) => handleAllChange(e.target.checked)}>
                                Select All
                            </Checkbox>
                        </Box>

                        {options.map((item) => (
                            <Box key={item.value} className={`${styles.ListItem} ${parentStyles?.ListItem}`}>
                                <Checkbox
                                    isChecked={selected.includes(item.value)}
                                    onChange={() => handleChange(item.value)}
                                    className={`${styles.Checkbox} ${parentStyles?.Checkbox}`}
                                >
                                    {item.name}
                                </Checkbox>
                            </Box>
                        ))}
                    </>
                ) : (
                    <Box className={`${styles.ListItem} ${parentStyles?.ListItem}`}>
                        No available options
                    </Box>
                )}
            </Flex>

            {confirm && isOpen && (
                <ConfirmModal
                    title={confirmTitle}
                    body={confirmBody}
                    closeModal={() => {
                        setIsOpen(false);
                        setSelected(oldSelected)
                    }}
                    onConfirmClick={() => {
                        onChange(selected);
                        setIsOpen(false);
                    }}
                />
            )}
        </>
    );
}

export default React.memo(Checkboxes)