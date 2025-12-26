// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"strings"
)

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Value   interface{}
	Message string
}

// Error implements the error interface
func (e *ValidationError) Error() string {
	return fmt.Sprintf("config validation failed for '%s': %s (got: %v)", e.Field, e.Message, e.Value)
}

// NewValidationError creates a new validation error
func NewValidationError(field string, value interface{}, message string) error {
	return &ValidationError{
		Field:   field,
		Value:   value,
		Message: message,
	}
}

// ValidationErrors represents multiple validation errors
type ValidationErrors struct {
	Errors []error
}

// Error implements the error interface
func (e *ValidationErrors) Error() string {
	if len(e.Errors) == 0 {
		return "no validation errors"
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d validation errors:\n", len(e.Errors)))
	for i, err := range e.Errors {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Error()))
	}
	return sb.String()
}

// Add adds an error to the collection
func (e *ValidationErrors) Add(err error) {
	if err != nil {
		e.Errors = append(e.Errors, err)
	}
}

// HasErrors returns true if there are any errors
func (e *ValidationErrors) HasErrors() bool {
	return len(e.Errors) > 0
}

// ToError returns the ValidationErrors as an error if there are errors, nil otherwise
func (e *ValidationErrors) ToError() error {
	if e.HasErrors() {
		return e
	}
	return nil
}

// Validator interface for configuration validation
type Validator interface {
	Validate() error
}

// ValidateAll validates all sub-configurations and returns combined errors
func ValidateAll(validators ...Validator) error {
	errs := &ValidationErrors{}
	for _, v := range validators {
		if err := v.Validate(); err != nil {
			errs.Add(err)
		}
	}
	return errs.ToError()
}
