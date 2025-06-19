package cluster

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var reTemplate = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Phase 1: Resolve nested key expressions like {{ {{env}}_{{host}}_url }}
func (cluster *Cluster) ResolveTemplateKeys(template string, data map[string]interface{}) (string, error) {
	missingKeys := map[string]bool{}

	for i := 0; i < cluster.Conf.TemplateVariableMaxDepth; i++ {
		changed := false

		template = reTemplate.ReplaceAllStringFunc(template, func(match string) string {
			keyExpr := strings.TrimSpace(match[2 : len(match)-2])

			// Resolve nested keys inside the keyExpr
			keyResolved := reTemplate.ReplaceAllStringFunc(keyExpr, func(innerMatch string) string {
				innerKey := strings.TrimSpace(innerMatch[2 : len(innerMatch)-2])
				if val, ok := data[innerKey]; ok {
					changed = true
					if strVal, ok := val.(string); ok {
						return strVal
					}
					return fmt.Sprintf("%v", val) // convert to string if not already
				}
				missingKeys[innerKey] = true
				return innerMatch
			})

			if keyResolved != keyExpr {
				changed = true
			}

			return "{{ " + keyResolved + " }}"
		})

		if !changed {
			break
		}
	}

	if len(missingKeys) > 0 {
		keys := make([]string, 0, len(missingKeys))
		for k := range missingKeys {
			keys = append(keys, k)
		}
		return "", fmt.Errorf("ResolveTemplateKeys: missing nested keys: %v", keys)
	}

	return template, nil
}

// Phase 2: Replace resolved template keys with their values, return error if some unresolved
func (cluster *Cluster) RenderTemplateValues(template string, data map[string]string) (string, error) {
	missingKeys := []string{}
	missingSet := map[string]bool{}

	result := reTemplate.ReplaceAllStringFunc(template, func(match string) string {
		key := strings.TrimSpace(match[2 : len(match)-2])
		if val, ok := data[key]; ok {
			return val
		}
		if !missingSet[key] {
			missingSet[key] = true
			missingKeys = append(missingKeys, key)
		}
		return match // leave unresolved
	})

	if len(missingKeys) > 0 {
		return result, fmt.Errorf("RenderTemplateValues: missing keys: %v", missingKeys)
	}

	return result, nil
}

func (cluster *Cluster) GetTemplateData() map[string]interface{} {
	result := make(map[string]interface{})
	domain := cluster.GetDomain()

	proxies := make([]string, 0)
	dbs := make([]string, 0)

	// Add default template data from cluster configuration
	for i, p := range cluster.Proxies {
		if p != nil {
			seq := strconv.Itoa(i + 1)
			host := p.GetHost()
			if !strings.Contains(host, domain) {
				host = host + "." + domain
			}
			proxies = append(proxies, host)
			result["database_proxy_"+seq+"_internal_fqdn_long"] = host
			result["database_proxy_"+seq+"_internal_fqdn_short"] = strings.ReplaceAll(host, cluster.GetDomain(), "")
		}
	}

	proxyHosts := strings.Join(proxies, ",")
	result["database_proxies_internal_fqdn_long"] = proxyHosts
	result["database_proxies_internal_fqdn_short"] = strings.ReplaceAll(proxyHosts, cluster.GetDomain(), "")

	for i, db := range cluster.Servers {
		if db != nil {
			seq := strconv.Itoa(i + 1)
			host := db.Host
			if !strings.Contains(host, domain) {
				host = host + "." + domain
			}
			dbs = append(dbs, host)
			result["database_"+seq+"_internal_fqdn_long"] = host
			result["database_"+seq+"_internal_fqdn_short"] = strings.ReplaceAll(host, cluster.GetDomain(), "")
		}
	}

	dbHosts := strings.Join(dbs, ",")
	result["databases_internal_fqdn_long"] = dbHosts
	result["databases_internal_fqdn_short"] = strings.ReplaceAll(dbHosts, cluster.GetDomain(), "")

	for _, app := range cluster.Apps {
		if app != nil {
			host := app.Host
			if !strings.Contains(host, domain) {
				host = host + "." + domain
			}
			result[app.Name+"_internal_fqdn_long"] = host
			result[app.Name+"_internal_fqdn_short"] = strings.ReplaceAll(host, cluster.GetDomain(), "")
			result[app.Name+"_external_fqdn"] = app.GetExternalFQDN()
		}
	}

	return result
}
