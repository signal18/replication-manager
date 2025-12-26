package config

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/version"
	"gopkg.in/ini.v1"
)

type StringsMap struct {
	*sync.Map
}

func (m *StringsMap) Get(key string) string {
	if v, ok := m.Load(key); ok {
		return v.(string)
	}
	return ""
}

func (m *StringsMap) CheckAndGet(key string) (string, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(string), true
	}
	return "", false
}

func (m *StringsMap) ToNormalMap(c map[string]string) {
	// clear old value
	c = make(map[string]string)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(string)
		return true
	})
}

func (m *StringsMap) ToNewMap() map[string]string {
	// clear old value
	c := make(map[string]string)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(string)
		return true
	})

	return c
}

func (m *StringsMap) Set(k string, v string) {
	m.Store(k, v)
}

func FromNormalStringMap(m *StringsMap, c map[string]string) *StringsMap {
	if m == nil {
		m = NewStringsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Store(k, v)
	}

	return m
}

func FromStringSyncMap(m *StringsMap, c *StringsMap) *StringsMap {
	if m == nil {
		m = NewStringsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Range(func(k any, v any) bool {
			m.Store(k.(string), v.(string))
			return true
		})
	}

	return m
}

func (m *StringsMap) Callback(f func(key, value any) bool) {
	//Insert all values to new map
	m.Range(f)
}

func (m *StringsMap) Clear() {
	//Insert all values to new map
	m.Range(func(key any, value any) bool {
		k := key.(string)
		m.Delete(k)
		return true
	})
}

func NewStringsMap() *StringsMap {
	s := new(sync.Map)
	m := &StringsMap{Map: s}
	return m
}

type UIntsMap struct {
	*sync.Map
}

func (m *UIntsMap) Get(key string) uint {
	if v, ok := m.Load(key); ok {
		return v.(uint)
	}
	return 0
}

func (m *UIntsMap) CheckAndGet(key string) (uint, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(uint), true
	}
	return 0, false
}

func (m *UIntsMap) ToNormalMap(c map[string]uint) {
	// clear old value
	c = make(map[string]uint)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(uint)
		return true
	})
}

func (m *UIntsMap) ToNewMap() map[string]uint {
	// clear old value
	c := make(map[string]uint)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(uint)
		return true
	})

	return c
}

func (m *UIntsMap) Set(k string, v uint) {
	m.Store(k, v)
}

func FromNormalUIntsMap(m *UIntsMap, c map[string]uint) *UIntsMap {
	if m == nil {
		m = NewUIntsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Store(k, v)
	}

	return m
}

func FromUIntSyncMap(m *UIntsMap, c *UIntsMap) *UIntsMap {
	if m == nil {
		m = NewUIntsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Range(func(k any, v any) bool {
			m.Store(k.(string), v.(uint))
			return true
		})
	}

	return m
}

func (m *UIntsMap) Callback(f func(key, value any) bool) {
	//Insert all values to new map
	m.Range(f)
}

func (m *UIntsMap) Clear() {
	//Insert all values to new map
	m.Range(func(key any, value any) bool {
		k := key.(string)
		m.Delete(k)
		return true
	})
}

func NewUIntsMap() *UIntsMap {
	s := new(sync.Map)
	m := &UIntsMap{Map: s}
	return m
}

type WorkLoadsMap struct {
	*sync.Map
}

func NewWorkLoadsMap() *WorkLoadsMap {
	s := new(sync.Map)
	m := &WorkLoadsMap{Map: s}
	return m
}

func (m *WorkLoadsMap) Get(key string) *WorkLoad {
	if v, ok := m.Load(key); ok {
		return v.(*WorkLoad)
	}
	return nil
}

func (m *WorkLoadsMap) GetOrNew(key string) *WorkLoad {
	if v, ok := m.Load(key); ok {
		return v.(*WorkLoad)
	}
	return new(WorkLoad)
}

func (m *WorkLoadsMap) CheckAndGet(key string) (*WorkLoad, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*WorkLoad), true
	}
	return nil, false
}

func (m *WorkLoadsMap) Set(key string, value *WorkLoad) {
	m.Store(key, value)
}

func (m *WorkLoadsMap) ToNormalMap(c map[string]*WorkLoad) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the WorkLoadsMap to the output map
	m.Callback(func(key string, value *WorkLoad) bool {
		c[key] = value
		return true
	})
}

func (m *WorkLoadsMap) ToNewMap() map[string]*WorkLoad {
	result := make(map[string]*WorkLoad)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*WorkLoad)
		return true
	})
	return result
}

func (m *WorkLoadsMap) Callback(f func(key string, value *WorkLoad) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*WorkLoad))
	})
}

func (m *WorkLoadsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalWorkLoadsMap(m *WorkLoadsMap, c map[string]*WorkLoad) *WorkLoadsMap {
	if m == nil {
		m = NewWorkLoadsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromWorkLoadsMap(m *WorkLoadsMap, c *WorkLoadsMap) *WorkLoadsMap {
	if m == nil {
		m = NewWorkLoadsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *WorkLoad) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type TasksMap struct {
	*sync.Map
}

func NewTasksMap() *TasksMap {
	s := new(sync.Map)
	m := &TasksMap{Map: s}
	return m
}

func (m *TasksMap) Get(key string) *Task {
	if v, ok := m.Load(key); ok {
		return v.(*Task)
	}
	return nil
}

func (m *TasksMap) CheckAndGet(key string) (*Task, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*Task), true
	}
	return nil, false
}

func (m *TasksMap) Set(key string, value *Task) {
	m.Store(key, value)
}

func (m *TasksMap) LoadOrStore(key string, value *Task) (*Task, bool) {
	v, ok := m.Map.LoadOrStore(key, value)
	return v.(*Task), ok
}

func (m *TasksMap) ToNormalMap(c map[string]*Task) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the TasksMap to the output map
	m.Callback(func(key string, value *Task) bool {
		c[key] = value
		return true
	})
}

func (m *TasksMap) ToNewMap() map[string]*Task {
	result := make(map[string]*Task)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*Task)
		return true
	})
	return result
}

func (m *TasksMap) Callback(f func(key string, value *Task) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*Task))
	})
}

func (m *TasksMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalTasksMap(m *TasksMap, c map[string]*Task) *TasksMap {
	if m == nil {
		m = NewTasksMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromTasksMap(m *TasksMap, c *TasksMap) *TasksMap {
	if m == nil {
		m = NewTasksMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *Task) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type VersionsMap struct {
	*sync.Map
}

func NewVersionsMap() *VersionsMap {
	s := new(sync.Map)
	m := &VersionsMap{Map: s}
	return m
}

func (m *VersionsMap) Get(key string) *version.Version {
	if v, ok := m.Load(key); ok {
		return v.(*version.Version)
	}
	return nil
}

func (m *VersionsMap) CheckAndGet(key string) (*version.Version, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*version.Version), true
	}
	return nil, false
}

func (m *VersionsMap) Set(key string, value *version.Version) {
	m.Store(key, value)
}

func (m *VersionsMap) ToNormalMap(c map[string]*version.Version) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the VersionsMap to the output map
	m.Callback(func(key string, value *version.Version) bool {
		c[key] = value
		return true
	})
}

func (m *VersionsMap) ToNewMap() map[string]*version.Version {
	result := make(map[string]*version.Version)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*version.Version)
		return true
	})
	return result
}

func (m *VersionsMap) Callback(f func(key string, value *version.Version) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*version.Version))
	})
}

func (m *VersionsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalVersionsMap(m *VersionsMap, c map[string]*version.Version) *VersionsMap {
	if m == nil {
		m = NewVersionsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromVersionsMap(m *VersionsMap, c *VersionsMap) *VersionsMap {
	if m == nil {
		m = NewVersionsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *version.Version) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type VarStateSorter []VariableState

func (a VarStateSorter) Len() int           { return len(a) }
func (a VarStateSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a VarStateSorter) Less(i, j int) bool { return a[i].VariableName < a[j].VariableName }

type SingleValue string

func (s SingleValue) String() string {
	return string(s)
}

func (s *SingleValue) Print(varname string) string {
	return fmt.Sprintf("%s=%s", varname, s.String())
}

func (s *SingleValue) Set(value string) {
	*s = SingleValue(value)
}

func (s *SingleValue) IsMap() bool {
	return false
}

func (s SingleValue) IsEqual(other VariableValue) bool {
	if o, ok := other.(*SingleValue); ok {
		return s == *o
	}
	return false
}

type MapValue map[string]string

func (mv MapValue) String() string {
	pairs := make([]string, 0, len(mv))
	for k, v := range mv {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, ",")
}

func (mv MapValue) Print(varname string) string {
	pairs := make([]string, 0, len(mv))
	for k, v := range mv {
		pairs = append(pairs, fmt.Sprintf("%s='%s=%s'", varname, k, v))
	}
	return strings.Join(pairs, "\n")
}

func (mv MapValue) Set(value string) {
	parts := strings.Split(value, ",")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			k := strings.TrimSpace(kv[0])
			vv := strings.TrimSpace(kv[1])
			mv[k] = vv
		}
	}
}

func (mv MapValue) IsMap() bool {
	return true
}

func (mv MapValue) IsEqual(other VariableValue) bool {
	o, ok := other.(MapValue)
	if !ok {
		return false
	}
	if len(mv) != len(o) {
		return false
	}
	for k, v := range mv {
		if ov, ok := o[k]; !ok || ov != v {
			return false
		}
	}
	return true
}

type VariableValue interface {
	String() string
	Print(varname string) string
	Set(value string)
	IsEqual(other VariableValue) bool
	IsMap() bool
}

type VariableState struct {
	VariableName string        `json:"variableName"`
	RuntimeName  string        `json:"runtimeName"`
	Config       VariableValue `json:"cfgValue"`
	Deployed     VariableValue `json:"value"`
	Runtime      VariableValue `json:"runtimeValue"`
	Preserved    VariableValue `json:"preservedValue"`
}

type LastConfigUpdate struct {
	Config   time.Time `json:"config"`
	Deployed time.Time `json:"deployed"`
}

func NewVariableState(varname string) *VariableState {
	return &VariableState{
		VariableName: strings.ToLower(varname),
		Config:       nil,
		Deployed:     nil,
		Preserved:    nil,
	}
}

func (v *VariableState) IsEqual() bool {
	if v.Config == nil && v.Deployed == nil {
		return true
	}
	if v.Config == nil || v.Deployed == nil {
		return false
	}

	return v.Config.String() == v.Deployed.String()
}

func (v *VariableState) IsPreserved() bool {
	if v.Preserved == nil {
		return false
	}

	// If the preserved value is different from the config value
	if !v.Preserved.IsEqual(v.Config) {
		return true
	}

	return false
}

func (v *VariableState) SetConfigValue(value string) {
	isMap := strings.Contains(value, "=")
	if v.Config == nil {
		if isMap {
			v.Config = make(MapValue)
		} else {
			v.Config = new(SingleValue)
		}
	}

	v.Config.Set(value)
}

func (v *VariableState) SetDeployedValue(value string) {
	isMap := strings.Contains(value, "=")
	if v.Deployed == nil {
		if isMap {
			v.Deployed = make(MapValue)
		} else {
			v.Deployed = new(SingleValue)
		}
	}

	v.Deployed.Set(value)
}

func (v *VariableState) SetRuntimeValue(value string) {
	isMap := strings.Contains(value, "=")
	if v.Runtime == nil {
		if isMap {
			v.Runtime = make(MapValue)
		} else {
			v.Runtime = new(SingleValue)
		}
	}

	v.Runtime.Set(value)
}

func (v *VariableState) SetPreservedValue(value string) {
	isMap := strings.Contains(value, "=")
	if v.Preserved == nil {
		if isMap {
			v.Preserved = make(MapValue)
		} else {
			v.Preserved = new(SingleValue)
		}
	}

	v.Preserved.Set(value)
}

func (v *VariableState) Print(conftype string) string {
	if conftype == "config" && v.Config != nil {
		return v.Config.Print(v.VariableName)
	} else if conftype == "deployed" && v.Deployed != nil {
		return v.Deployed.Print(v.VariableName)
	} else if conftype == "runtime" && v.Runtime != nil {
		return v.Runtime.Print(v.VariableName)
	} else if conftype == "preserved" && v.Preserved != nil {
		return v.Preserved.Print(v.VariableName)
	}
	return ""
}

type VariablesMap struct {
	*sync.Map
}

func NewVariablesMap() *VariablesMap {
	s := new(sync.Map)
	m := &VariablesMap{Map: s}
	return m
}

func (m *VariablesMap) Get(key string) *VariableState {
	if v, ok := m.Load(key); ok {
		return v.(*VariableState)
	}
	return nil
}

func (m *VariablesMap) CheckAndGet(key string) (*VariableState, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*VariableState), true
	}
	return nil, false
}

func (m *VariablesMap) Set(key string, value *VariableState) {
	m.Store(key, value)
}

func (m *VariablesMap) ToNormalMap(c map[string]*VariableState) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the VariablesMap to the output map
	m.Callback(func(key string, value *VariableState) bool {
		c[key] = value
		return true
	})
}

func (m *VariablesMap) ToNewMap() map[string]*VariableState {
	result := make(map[string]*VariableState)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*VariableState)
		return true
	})
	return result
}

func (m *VariablesMap) Callback(f func(key string, value *VariableState) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*VariableState))
	})
}

func (m *VariablesMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalVariablesMap(m *VariablesMap, c map[string]*VariableState) *VariablesMap {
	if m == nil {
		m = NewVariablesMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromVariablesMap(m *VariablesMap, c *VariablesMap) *VariablesMap {
	if m == nil {
		m = NewVariablesMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *VariableState) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

func (m *VariablesMap) EmptyDeployedValues() {
	m.Range(func(key, value any) bool {
		if state, ok := value.(*VariableState); ok {
			state.Deployed = nil
		}
		return true
	})
}

func (m *VariablesMap) EmptyConfigValues() {
	m.Range(func(key, value any) bool {
		if state, ok := value.(*VariableState); ok {
			state.Config = nil
		}
		return true
	})
}

func (m *VariablesMap) EmptyRuntimeValues() {
	m.Range(func(key, value any) bool {
		if state, ok := value.(*VariableState); ok {
			state.Runtime = nil
		}
		return true
	})
}

func (m *VariablesMap) EmptyPreservedValues() {
	m.Range(func(key, value any) bool {
		if state, ok := value.(*VariableState); ok {
			state.Preserved = nil
		}
		return true
	})
}

func (m *VariablesMap) SetDeployedValue(varname string, value string) {
	if state, ok := m.Load(varname); ok {
		state.(*VariableState).SetDeployedValue(value)
	} else {
		state := NewVariableState(varname)
		state.SetDeployedValue(value)
		m.Store(varname, state)
	}
}

func (m *VariablesMap) SetDeployedValues(strmap map[string]string) {
	for k, v := range strmap {
		m.SetDeployedValue(k, v)
	}
}

func (m *VariablesMap) SetRuntimeValue(varname string, value string) {
	if state, ok := m.Load(varname); ok {
		state.(*VariableState).SetRuntimeValue(value)
	} else {
		state := NewVariableState(varname)
		state.SetRuntimeValue(value)
		m.Store(varname, state)
	}
}

func (m *VariablesMap) SetRuntimeValues(strmap map[string]string) {
	for k, v := range strmap {
		m.SetRuntimeValue(k, v)
	}
}

func (m *VariablesMap) SetConfigValue(varname string, value string) {
	if state, ok := m.Load(varname); ok {
		state.(*VariableState).SetConfigValue(value)
	} else {
		state := NewVariableState(varname)
		state.SetConfigValue(value)
		m.Store(varname, state)
	}
}

func (m *VariablesMap) SetConfigValues(strmap map[string]string) {
	for k, v := range strmap {
		m.SetConfigValue(k, v)
	}
}

func (m *VariablesMap) SetPreservedValue(varname string, value string) {
	if state, ok := m.Load(varname); ok {
		state.(*VariableState).SetPreservedValue(value)
	} else {
		state := NewVariableState(varname)
		state.SetPreservedValue(value)
		m.Store(varname, state)
	}
}

func (m *VariablesMap) SetPreservedValues(strmap map[string]string) {
	for k, v := range strmap {
		m.SetPreservedValue(k, v)
	}
}

func (m *VariablesMap) ToNormalDeployedMap() map[string]string {
	result := make(map[string]string)
	m.Range(func(k, v any) bool {
		val := v.(VariableState)
		if val.Deployed == nil {
			return true
		}

		result[k.(string)] = val.Deployed.String()
		return true
	})
	return result
}

func (m *VariablesMap) ToNormalConfigMap() map[string]string {
	result := make(map[string]string)
	m.Range(func(k, v any) bool {
		val := v.(VariableState)
		if val.Config == nil {
			return true
		}

		result[k.(string)] = val.Config.String()
		return true
	})
	return result
}

func (m *VariablesMap) GetVariables(diff bool) []VariableState {
	result := make([]VariableState, 0)
	m.Range(func(k, v any) bool {
		val := v.(*VariableState)

		if !diff || !val.IsEqual() {
			result = append(result, *val)
		}

		return true
	})
	return result
}

func (m *VariablesMap) LoadFromConfigFile(path string, cnftype string) error {
	if cnftype != "config" && cnftype != "deployed" && cnftype != "preserved" {
		return fmt.Errorf("invalid config type: %s", cnftype)
	}

	// Allow shadows to handle multiple same options
	cfgFile, err := ini.LoadSources(ini.LoadOptions{AllowShadows: true}, path)
	if err != nil {
		return err
	}

	section := cfgFile.Section("mysqld")
	for _, key := range section.Keys() {
		varname := strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(key.Name(), "loose_")), "-", "_")
		if varname == "optimizer_switch" {
			values := key.ValueWithShadows()
			for _, v := range values {
				if cnftype == "config" {
					m.SetConfigValue(varname, v)
				} else if cnftype == "deployed" {
					m.SetDeployedValue(varname, v)
				} else if cnftype == "preserved" {
					m.SetPreservedValue(varname, v)
				}
			}
		} else {
			if cnftype == "config" {
				m.SetConfigValue(varname, key.Value())
			} else if cnftype == "deployed" {
				m.SetDeployedValue(varname, key.Value())
			} else if cnftype == "preserved" {
				m.SetPreservedValue(varname, key.Value())
			}
		}
	}

	return nil
}
