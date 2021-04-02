package config_store

import (
	"errors"
	"fmt"
	"log"
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

func NewProperty(section []string, namespace string, env Environment, key string, values []*Value) *Property {
	p := &Property{
		Key:         key,
		Values:      values,
		Section:     section,
		Namespace:   namespace,
		Environment: env,
	}
	return p
}

func NewStringProperty(section []string, namespace string, env Environment, key string, value string) *Property {
	v := &Value{
		Type:        Type_STRING,
		ValueString: value,
	}

	values := make([]*Value, 0)
	values = append(values, v)
	return NewProperty(section, namespace, env, key, values)
}

func (p *Property) Validate() error {
	if p.Key == "" {
		return ErrPropertyKeyMissing
	}

	return nil
}

func (p *Property) SetStringValue(v string) {
	p.Values = make([]*Value, 0)
	p.Values = append(p.Values, &Value{
		ValueString: v,
	})
}

func (p *Property) SetValue(value interface{}) {
	p.Values = make([]*Value, 0)
	if v, ok := value.(string); ok {
		p.Values = append(p.Values, &Value{
			Type:        Type_STRING,
			ValueString: v,
		})
		return
	}

	if v, ok := value.(int); ok {
		p.Values = append(p.Values, &Value{
			Type:     Type_INT,
			ValueInt: int64(v),
		})

		return
	}

	if v, ok := value.(int64); ok {
		p.Values = append(p.Values, &Value{
			Type:     Type_INT,
			ValueInt: v,
		})

		return
	}

	if v, ok := value.(float32); ok {
		p.Values = append(p.Values, &Value{
			Type:       Type_FLOAT,
			ValueFloat: v,
		})

		return
	}

	if v, ok := value.(bool); ok {
		p.Values = append(p.Values, &Value{
			Type:      Type_BOOL,
			ValueBool: v,
		})

		return
	}
}

// DatabaseValue returns a string representation of the possible repeated Value
// property as a string.
// SQLite stores data natively as TEXT anyway
func (p *Property) DatabaseValue() string {
	buf := make([]string, 0)
	for _, v := range p.Values {
		buf = append(buf, v.DatabaseValue())
	}

	return strings.Join(buf, RecordSeperator)
}

// DatabaseValue returns a string representation where the first digit signifies
// the type for the Value.
func (v *Value) DatabaseValue() string {
	var s strings.Builder
	s.WriteString(strconv.FormatInt(int64(v.Type), 10))
	s.WriteString(v.GetStringValue())
	return s.String()
}

func ValueFromDatabaseValue(db string) *Value {
	t := db[0:1]
	v := db[1:]
	log.Printf("t: %v, v: %v", t, v)

	var value *Value

	if n, err := strconv.Atoi(t); err == nil {
		value = &Value{
			Type: Type(n),
		}

		switch value.Type {
		case Type_STRING:
			value.ValueString = v
		case Type_INT:
			if iv, err := strconv.Atoi(v); err == nil {
				value.ValueInt = int64(iv)
			} else {
				panic("stored value is not an integer")
			}
		case Type_FLOAT:
			if f, err := strconv.ParseFloat(v, 32); err == nil {
				value.ValueFloat = float32(f)
			} else {
				panic("stored value is not a float")
			}
		case Type_BOOL:
			if v == "true" {
				value.ValueBool = true
			} else {
				value.ValueBool = false
			}
		}

	} else {
		panic("first character of value is not an integer")
	}

	return value
}

func (v *Value) GetStringValue() string {
	switch v.Type {
	case Type_STRING:
		return v.ValueString
	case Type_INT:
		return strconv.FormatInt(v.ValueInt, 10)
	case Type_FLOAT:
		return fmt.Sprintf("%f", v.ValueFloat)
	case Type_BOOL:
		if v.ValueBool {
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
		return v.ValueString
	case Type_INT:
		return v.ValueInt
	case Type_FLOAT:
		return v.ValueFloat
	case Type_BOOL:
		return v.ValueBool
	}

	return nil
}

func (p *Property) Scan(results map[string]interface{}) error {
	if buf, ok := results["key"]; ok {
		if buf != nil {
			p.Key = buf.(string)
		}
	}

	if buf, ok := results["value"]; ok {
		if buf != nil {
			values := make([]*Value, 0)
			vs := strings.Split(buf.(string), RecordSeperator)
			for _, v := range vs {
				values = append(values, ValueFromDatabaseValue(v))
			}

			p.Values = values
		}
	}

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

	return nil
}
