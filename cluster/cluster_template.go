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
	depthExceeded := false

	resolved := cluster.ResolveTemplateKeysRecursive(template, data, &missingKeys, &depthExceeded, 0)
	if depthExceeded {
		return resolved, fmt.Errorf("depth exceeded")
	}

	if len(missingKeys) > 0 {
		missingKeysList := make([]string, 0, len(missingKeys))
		for key := range missingKeys {
			missingKeysList = append(missingKeysList, key)
		}
		return resolved, fmt.Errorf("missing keys: %v", missingKeysList)
	}

	return resolved, nil
}

func (cluster *Cluster) ResolveTemplateKeysRecursive(template string, data map[string]string, missingKeys *map[string]bool, depthExceeded *bool, depth int) string {
	return reTemplate.ReplaceAllStringFunc(template, func(match string) string {
		keyExpr := strings.TrimSpace(match[2 : len(match)-2])

		if strings.Contains(keyExpr, "{{") && strings.Contains(keyExpr, "}}") {
			// Nested template key, resolve it recursively
			if depth <= cluster.Conf.TemplateVariableMaxDepth {
				result := cluster.ResolveTemplateKeysRecursive(keyExpr, data, missingKeys, depthExceeded, depth+1)
				if depth == 0 && !strings.HasPrefix(result, "{{") {
					return "{{" + result + "}}"
				} else {
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
			if !(*missingKeys)[keyExpr] {
				(*missingKeys)[keyExpr] = true
			}
		}

		return match // leave unresolved
	})
}

// Phase 2: Replace resolved template keys with their values, return error if some unresolved
func (cluster *Cluster) OpenSVCRenderTemplate(template string, data map[string]string) (string, error) {
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

func (cluster *Cluster) GetTemplateData(basemap map[string]string) map[string]string {
	if basemap == nil {
		basemap = make(map[string]string)
	}

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
