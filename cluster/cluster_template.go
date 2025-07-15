package cluster

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

var reTemplate = regexp.MustCompile(`\{\{\s*((?:\{\{[^{}]+\}\}|[^{}])+?)\s*\}\}`)

// OpenSVCParseTemplate parses a template string with placeholders in the format {{key}}.
func (cluster *Cluster) ParseAppTemplate(template string, data string) (string, error) {
	missingKeys := []string{}
	missingSet := map[string]bool{}

	result := reTemplate.ReplaceAllStringFunc(template, func(match string) string {
		key := strings.TrimSpace(match[2 : len(match)-2])
		if key == "" {
			return match // leave unresolved if empty key
		}

		res := gjson.Get(data, key).Value()
		if res == nil {
			if !missingSet[key] {
				missingKeys = append(missingKeys, key)
				missingSet[key] = true
			}
			return match // leave unresolved if key not found
		} else if str, ok := res.(string); ok {
			return str // return the resolved value
		} else if num, ok := res.(float64); ok {
			return strconv.FormatFloat(num, 'f', -1, 64) // return number as string
		} else if boolVal, ok := res.(bool); ok {
			if boolVal {
				return "true" // return boolean as string
			}
			return "false"
		} else if arr, ok := res.([]interface{}); ok {
			// If it's an array, join the elements with commas
			strArr := make([]string, len(arr))
			for i, v := range arr {
				if str, ok := v.(string); ok {
					strArr[i] = str
				} else if num, ok := v.(float64); ok {
					strArr[i] = strconv.FormatFloat(num, 'f', -1, 64)
				} else if boolVal, ok := v.(bool); ok {
					if boolVal {
						strArr[i] = "true"
					} else {
						strArr[i] = "false"
					}
				} else {
					return match // leave unresolved if unsupported type
				}
			}
			return strings.Join(strArr, ", ") // join array elements
		} else {
			return match // leave unresolved if unsupported type
		}
	})

	if len(missingKeys) > 0 {
		return result, fmt.Errorf("OpenSVCParseTemplate: missing keys: %v", missingKeys)
	}

	return result, nil
}

// GetTemplateData returns a map of template data for the cluster, including database and app information.
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
