package cluster

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
)

var reTemplate = regexp.MustCompile(`\{\{\s*((?:\{\{[^{}]+\}\}|[^{}])+?)\s*\}\}`)

// Phase 1: Resolve nested key expressions like {{ {{env}}_{{host}}_url }}
func (cluster *Cluster) ResolveTemplateKeys(template string, data map[string]string) (string, error) {
	missingKeys := map[string]bool{}
	cache := map[string]string{}
	depthExceeded := false
	// First pass to resolve deepest nested keys
	resolved, depth := cluster.ResolveTemplateKeysRecursive(template, data, cache, missingKeys, &depthExceeded, 0)

	// If depth is greater than 0, we need to resolve nested keys multiple times
	if depth > 0 {
		for i := 0; i < depth; i++ {
			resolved, _ = cluster.ResolveTemplateKeysRecursive(resolved, data, cache, missingKeys, &depthExceeded, 0)
		}
	}

	if depthExceeded || len(missingKeys) > 0 {
		missingKeysList := make([]string, 0, len(missingKeys))
		for key := range missingKeys {
			missingKeysList = append(missingKeysList, key)
		}

		if depthExceeded {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "ResolveTemplateKeys: depth exceeded for template: %s", template)
		}
		return resolved, fmt.Errorf("ResolveTemplateKeys: missing keys or depth exceeded: [%s]", strings.Join(missingKeysList, ", "))
	}

	return resolved, nil
}

func (cluster *Cluster) ResolveTemplateKeysRecursive(template string, data map[string]string, cache map[string]string, missingKeys map[string]bool, depthExceeded *bool, depth int) (string, int) {
	deepest := depth
	resolved := reTemplate.ReplaceAllStringFunc(template, func(match string) string {
		keyExpr := strings.TrimSpace(match[2 : len(match)-2])
		if val, ok := cache[keyExpr]; ok {
			// If the key is already resolved, return it directly
			return val
		}

		if strings.Contains(keyExpr, "{{") && strings.Contains(keyExpr, "}}") {
			// Nested template key, resolve it recursively
			if depth <= cluster.Conf.TemplateVariableMaxDepth {
				result, rdeep := cluster.ResolveTemplateKeysRecursive(keyExpr, data, cache, missingKeys, depthExceeded, depth+1)
				if rdeep > deepest {
					deepest = rdeep
				}

				if depth == 0 && !strings.HasPrefix(result, "{{") {
					return "{{" + result + "}}"
				} else {
					// cache nested result
					if !strings.HasPrefix(result, "{{") && !strings.HasSuffix(result, "}}") {
						// Add to cache if not already resolved
						if _, exists := cache[keyExpr]; !exists {
							cache[keyExpr] = result
						}
					}
					return result
				}
			} else {
				*depthExceeded = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "ResolveTemplateKeys: depth exceeded for key: %s", keyExpr)
				return match
			}
		}

		// Simple key, check if it exists in data
		val, ok := data[keyExpr]
		if ok && depth > 0 {
			return val
		} else if !ok {
			// If the key is not found, add it to missing keys
			if !missingKeys[keyExpr] {
				missingKeys[keyExpr] = true
			}
		}

		return match // leave unresolved
	})

	return resolved, deepest
}

// Phase 2: Replace resolved template keys {{key}} to opensvc template syntax {[prefix.]key}
func (cluster *Cluster) OpenSVCParseTemplate(template string, data map[string]string, prefix string) (string, error) {
	if data == nil {
		return template, fmt.Errorf("OpenSVCParseTemplate: data map is nil")
	}

	missingKeys := []string{}
	missingSet := map[string]bool{}

	result := reTemplate.ReplaceAllStringFunc(template, func(match string) string {
		key := strings.TrimSpace(match[2 : len(match)-2])
		if _, ok := data[key]; ok {
			if prefix != "" {
				return fmt.Sprintf("{%s.%s}", prefix, key)
			} else {
				return fmt.Sprintf("{%s}", key)
			}
		}
		if !missingSet[key] {
			missingSet[key] = true
			missingKeys = append(missingKeys, key)
		}
		return match // leave unresolved
	})

	if len(missingKeys) > 0 {
		return result, fmt.Errorf("OpenSVCParseTemplate: missing keys: %v", missingKeys)
	}

	return result, nil
}

func (cluster *Cluster) GetTemplateData() map[string]string {
	basemap := make(map[string]string)

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
			basemap["database_proxy_"+seq+"_internal_fqdn_long"] = host
			basemap["database_proxy_"+seq+"_internal_fqdn_short"] = strings.ReplaceAll(host, cluster.GetDomain(), "")
		}
	}

	proxyHosts := strings.Join(proxies, ",")
	basemap["database_proxies_internal_fqdn_long"] = proxyHosts
	basemap["database_proxies_internal_fqdn_short"] = strings.ReplaceAll(proxyHosts, cluster.GetDomain(), "")

	for i, db := range cluster.Servers {
		if db != nil {
			seq := strconv.Itoa(i + 1)
			host := db.Host
			if !strings.Contains(host, domain) {
				host = host + "." + domain
			}
			dbs = append(dbs, host)
			basemap["database_"+seq+"_internal_fqdn_long"] = host
			basemap["database_"+seq+"_internal_fqdn_short"] = strings.ReplaceAll(host, cluster.GetDomain(), "")
		}
	}

	dbHosts := strings.Join(dbs, ",")
	basemap["databases_internal_fqdn_long"] = dbHosts
	basemap["databases_internal_fqdn_short"] = strings.ReplaceAll(dbHosts, cluster.GetDomain(), "")

	for _, app := range cluster.Apps {
		if app != nil {
			host := app.Host
			if !strings.Contains(host, domain) {
				host = host + "." + domain
			}
			basemap[app.Name+"_internal_fqdn_long"] = host
			basemap[app.Name+"_internal_fqdn_short"] = strings.ReplaceAll(host, cluster.GetDomain(), "")
			basemap[app.Name+"_external_fqdn"] = app.GetExternalFQDN()
		}
	}

	return basemap
}
