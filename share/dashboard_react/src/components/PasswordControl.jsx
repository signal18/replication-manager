import React, { forwardRef, useRef } from 'react'
import {
  FormControl,
  FormLabel,
  FormErrorMessage,
  InputGroup,
  InputRightElement,
  useDisclosure,
  useMergeRefs,
  Input
} from '@chakra-ui/react'
import { HiEye, HiEyeOff } from 'react-icons/hi'
import RMIconButton from './RMIconButton'

const PasswordControl = forwardRef((props, ref) => {
  const { isOpen, onToggle } = useDisclosure()
  const inputRef = useRef(null)

  const mergeRef = useMergeRefs(inputRef, ref)
  const onClickReveal = () => {
    onToggle()
    if (inputRef.current) {
      inputRef.current.focus({ preventScroll: true })
    }
  }

  return (
    <FormControl isInvalid={props.passwordError}>
      <FormLabel htmlFor='password'>Password</FormLabel>
      <InputGroup>
        <InputRightElement>
          <RMIconButton
            className={props.className}
            variant='text'
            aria-label={isOpen ? 'Mask password' : 'Reveal password'}
            icon={isOpen ? HiEyeOff : HiEye}
            onClick={onClickReveal}
          />
        </InputRightElement>
        <Input
          id='password'
          ref={mergeRef}
          name='password'
          onChange={props.onChange}
          type={isOpen ? 'text' : 'password'}
          autoComplete='current-password'
          required
          {...props}
        />
      </InputGroup>
      <FormErrorMessage className={props.errorMessage}>{props.passwordError}</FormErrorMessage>
    </FormControl>
  )
})

export default PasswordControl
