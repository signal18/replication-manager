import { Input, InputGroup, InputRightAddon } from "@chakra-ui/react"
import CustomIcon from "../Icons/CustomIcon"
import { AiOutlineSearch } from "react-icons/ai"

function SearchBox({ className, size, value, placeholder, onChange, onSearch }) {
    return (
        <InputGroup className={className}>
            <Input pl={2} size={size} type="text" placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value || "")} onKeyDown={(e) => e.code == "Enter" && onSearch()} />
            <InputRightAddon cursor="pointer" onClick={onSearch}>
                <CustomIcon icon={AiOutlineSearch} />
            </InputRightAddon>
        </InputGroup>
    )
}

export default SearchBox
