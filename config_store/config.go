package config_store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrPropertyKeyMissing = errors.New("property key is not set")
)

const (
	RecordSeperator = "\u241E"
)

func NewProperty(section []string, namespace string, env Environment, key string, values ...interface{}) *Property {
	p := &Property{
		Key:         key,
		Section:     section,
		Namespace:   namespace,
		Environment: env,
	}
	p.SetValues(values...)
	return p
}

func ValuesEqual(a []*Value, b []*Value) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].Data != b[i].Data {
			return false
		}
	}

	return true
}

func (p *Property) Validate() error {
	if p.Key == "" {
		return ErrPropertyKeyMissing
	}

	return nil
}

func NewValue(value interface{}) *Value {
	if v, ok := value.(string); ok {
		// TODO: maybe add functionality to check if it's an int/bool/float
		return &Value{
			Type: Type_STRING,
			Data: v,
		}
	}

	if v, ok := value.(int); ok {
		return &Value{
			Type: Type_INT,
			Data: fmt.Sprintf("%d", v),
		}
	}

	if v, ok := value.(int64); ok {
		return &Value{
			Type: Type_INT,
			Data: fmt.Sprintf("%d", v),
		}
	}

	if v, ok := value.(float32); ok {
		return &Value{
			Type: Type_FLOAT,
			Data: fmt.Sprintf("%f", v),
		}
	}

	if v, ok := value.(float64); ok {
		return &Value{
			Type: Type_FLOAT,
			Data: fmt.Sprintf("%f", v),
		}
	}

	if v, ok := value.(bool); ok {
		val := "true"
		if v == false {
			val = "false"
		}
		return &Value{
			Type: Type_BOOL,
			Data: val,
		}
	}

	return nil
}

func (p *Property) SetValues(values ...interface{}) {
	p.Values = make([]*Value, 0)
	for _, value := range values {
		p.Values = append(p.Values, NewValue(value))
	}
}

func (v *Value) GetStringValue() string {
	buf := v.GetValue()
	switch v.Type {
	case Type_STRING:
		return buf.(string)
	case Type_INT:
		return strconv.FormatInt(buf.(int64), 10)
	case Type_FLOAT:
		return fmt.Sprintf("%f", buf.(float64))
	case Type_BOOL:
		if buf.(bool) {
			return "true"
		} else {
			return "false"
		}
	}

	return ""
}

func (v *Value) GetValue() interface{} {
	switch v.Type {
	case Type_STRING:
		return v
	case Type_INT:
		if iv, err := strconv.Atoi(v.Data); err == nil {
			return int64(iv)
		} else {
			panic("stored value is not an integer")
		}
	case Type_FLOAT:
		if f, err := strconv.ParseFloat(v.Data, 32); err == nil {
			return float32(f)
		} else {
			panic("stored value is not a float")
		}
	case Type_BOOL:
		if v.Data == "true" {
			return true
		} else {
			return false
		}
	}

	return nil
}

func (p *Property) Scan(results map[string]interface{}) error {
	if buf, ok := results["key"]; ok {
		if buf != nil {
			p.Key = buf.(string)
		}
	}

	var order int64
	if buf, ok := results["val_order"]; ok {
		if buf != nil {
			order = buf.(int64)
		}
	}

	var valueType Type

	if buf, ok := results["type"]; ok {
		if buf != nil {
			valueType = Type(buf.(int64))
		}
	}

	if buf, ok := results["value"]; ok {
		if buf != nil {
			value := &Value{
				Data: buf.(string),
				Type: valueType,
			}

			p.Values = append(p.Values, value)
		}
	}

	if order == 0 {
		if buf, ok := results["namespace"]; ok {
			if buf != nil {
				p.Namespace = buf.(string)
			}
		}

		if buf, ok := results["section"]; ok {
			if buf != nil {
				s := buf.(string)
				if s != "" {
					p.Section = strings.Split(s, "|")
				}
			}
		}

		if buf, ok := results["environment"]; ok {
			if buf != nil {
				p.Environment = Environment(buf.(int64))
			}
		}

		if buf, ok := results["revision"]; ok {
			if buf != nil {
				p.Revision = buf.(int64)
			}
		}

		if buf, ok := results["version"]; ok {
			if buf != nil {
				p.Version = buf.(string)
			}
		}

		if buf, ok := results["created"]; ok {
			if buf != nil {
				t, err := time.Parse(time.RFC3339Nano, buf.(string))
				if err != nil {
					return err
				}
				p.Created = timestamppb.New(t)
			}
		}

		if buf, ok := results["deleted"]; ok {
			if buf != nil {
				t, err := time.Parse(time.RFC3339Nano, buf.(string))
				if err != nil {
					return err
				}
				p.Deleted = timestamppb.New(t)
			}
		}
	}

	return nil
}
