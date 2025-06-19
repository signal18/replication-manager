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
func (cluster *Cluster) ResolveTemplateKeys(template string, data map[string]interface{}) (string, error) {
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

func (cluster *Cluster) ResolveTemplateKeysRecursive(template string, data map[string]interface{}, missingKeys *map[string]bool, depthExceeded *bool, depth int) string {
	return reTemplate.ReplaceAllStringFunc(template, func(match string) string {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "ResolveTemplateKeys: match: %s", match)
		keyExpr := strings.TrimSpace(match[2 : len(match)-2])

		if strings.Contains(keyExpr, "{{") && strings.Contains(keyExpr, "}}") {
			// Nested template key, resolve it recursively
			if depth <= cluster.Conf.TemplateVariableMaxDepth {
				return "{{" + cluster.ResolveTemplateKeysRecursive(keyExpr, data, missingKeys, depthExceeded, depth+1) + "}}"
			} else {
				*depthExceeded = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "ResolveTemplateKeys: depth exceeded for key: %s", keyExpr)
				return match
			}
		}

		// Simple key, check if it exists in data
		val, ok := data[keyExpr]
		if ok && depth > 0 {
			return fmt.Sprintf("%v", val)
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
