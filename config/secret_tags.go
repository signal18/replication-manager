// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "reflect"

// A secret is declared on its field by the tag secret:"true": the cluster's and the server's
// settings (Config), an app's own settings (AppConfig). The lists, the API masks and the copies
// to the credential stores are read from the tag, never from a hand list.

// secretMask replaces a secret's value on a copy for the wire.
const secretMask = "*****"

// AppSecretKeys are an app's own settings that are secrets (its template's secret variables are
// typed secret on the variable).
var AppSecretKeys = SecretFieldKeys(reflect.TypeOf(AppConfig{}))

// SecretFieldKeys are the setting names (mapstructure) of the fields of typ tagged
// secret:"true", in field order.
func SecretFieldKeys(typ reflect.Type) []string {
	var keys []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Tag.Get("secret") == "true" {
			if key := f.Tag.Get("mapstructure"); key != "" {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// secretFields calls fn with the setting name and the field of every string field of the
// struct ptr points to tagged secret:"true".
func secretFields(ptr interface{}, fn func(key string, field reflect.Value)) {
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return
	}
	v = v.Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Tag.Get("secret") == "true" && f.Type.Kind() == reflect.String {
			fn(f.Tag.Get("mapstructure"), v.Field(i))
		}
	}
}

// MaskSecretFields masks, on the copy ptr points to, every secret field that is set: a copy
// for the wire never carries a secret, even encrypted.
func MaskSecretFields(ptr interface{}) {
	secretFields(ptr, func(_ string, field reflect.Value) {
		if field.String() != "" && field.CanSet() {
			field.SetString(secretMask)
		}
	})
}

// SecretValues are the app's secret settings that are set, as stored (encrypted), by setting
// name.
func (cfg *AppConfig) SecretValues() map[string]string {
	out := map[string]string{}
	if cfg == nil {
		return out
	}
	secretFields(cfg, func(key string, field reflect.Value) {
		if field.String() != "" {
			out[key] = field.String()
		}
	})
	return out
}
