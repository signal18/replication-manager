import React from 'react'
import { FormControl, FormLabel, FormErrorMessage, Input, Stack } from '@chakra-ui/react'
import PasswordControl from './PasswordControl'

// LoginForm is the ONE credentials form (username + password) shared by the login page
// (Pages/Login) and the admin-mode step-up modal (AdminModeModal, #2262), so there is a single
// login form to follow instead of two hand-rolled copies. Presentational only: the parent owns the
// username/password state and the submit, so each caller keeps its own button(s) and submit path
// (the login page signs in via redux; the modal gets an in-memory admin token).
//
// SetCredentialsModal is a different concern — setting/changing a stored credential, not signing in
// — and deliberately does not use this.
function LoginForm({
  username,
  password,
  onUsernameChange,
  onPasswordChange,
  usernameError,
  passwordError,
  onSubmit,
  usernameLabel = 'Username or Email',
  inputClassName,
  labelClassName,
  errorClassName,
  revealClassName
}) {
  // Pass the event through: the login page's handler calls e.preventDefault(); the modal's
  // handler ignores the argument.
  const onEnter = (e) => {
    if (e.key === 'Enter' && onSubmit) {
      onSubmit(e)
    }
  }
  return (
    <Stack spacing='5'>
      <FormControl isInvalid={!!usernameError}>
        <FormLabel className={labelClassName} htmlFor='username'>{usernameLabel}</FormLabel>
        <Input
          id='username'
          type='text'
          autoComplete='username'
          value={username}
          className={inputClassName}
          onChange={onUsernameChange}
          onKeyDown={onEnter}
        />
        <FormErrorMessage className={errorClassName}>{usernameError}</FormErrorMessage>
      </FormControl>
      <PasswordControl
        value={password}
        passwordError={passwordError}
        inputClassName={inputClassName}
        labelClassName={labelClassName}
        errorMessage={errorClassName}
        className={revealClassName}
        onChange={onPasswordChange}
        onKeyDown={onEnter}
      />
    </Stack>
  )
}

export default LoginForm
