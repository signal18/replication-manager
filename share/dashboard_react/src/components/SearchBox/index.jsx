import { Input, InputGroup, InputRightElement } from "@chakra-ui/react"
import CustomIcon from "../Icons/CustomIcon"
import { AiOutlineSearch } from "react-icons/ai"

function SearchBox({ className, size, value, placeholder, onChange, onSearch }) {
    return (
        <InputGroup size={size} className={className}>
            <Input pl={2} pr="2.5rem" type="text" placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value || "")} onKeyDown={(e) => e.code == "Enter" && onSearch()} />
            {/* Icon inside the field: no addon box, no border, theme-neutral */}
            <InputRightElement cursor="pointer" onClick={onSearch}>
                <CustomIcon icon={AiOutlineSearch} />
            </InputRightElement>
        </InputGroup>
    )
}

export default SearchBox
