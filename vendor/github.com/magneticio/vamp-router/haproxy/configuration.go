package haproxy

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"sync"
	"text/template"
)

const (
	SEPARATOR = "::"
)

// Load a config from disk
func (c *Config) GetConfigFromDisk() error {
	if s, err := ioutil.ReadFile(c.JsonFile); err != nil {
		return err
	} else {
		if err := json.Unmarshal(s, &c); err != nil {
			return err
		}
	}

	c.Mutex = new(sync.RWMutex)
	return nil
}

func (c *Config) InitializeConfig() {
	c.Frontends = []*Frontend{}
	c.Backends = []*Backend{}
	c.Routes = []Route{}
	c.Mutex = new(sync.RWMutex)
}

// updates the weight of a server of a specific backend with a new weight
func (c *Config) SetWeight(backend string, server string, weight int) *Error {

	for _, be := range c.Backends {
		if be.Name == backend {
			for _, srv := range be.Servers {
				if srv.Name == server {
					srv.Weight = weight
					return nil
				}
			}
		}
	}

	return &Error{404, errors.New("no server found")}
}

// the transactions methods are kept separate so we can chain an arbitrary set of operations
// on the Config object within one transaction. Alas, this burdons the developer with extra housekeeping
// but gives you more control over the flow of mutations and reads without risking deadlocks or duplicating
// locks and unlocks inside of methods.
func (c *Config) BeginWriteTrans() {
	c.Mutex.Lock()
}

func (c *Config) EndWriteTrans() {
	c.Mutex.Unlock()
}

func (c *Config) BeginReadTrans() {
	c.Mutex.RLock()
}

func (c *Config) EndReadTrans() {
	c.Mutex.RUnlock()
}

// gets all frontends
func (c *Config) GetFrontends() []*Frontend {
	return c.Frontends
}

//updates the whole config in one go
func (c *Config) UpdateConfig(config *Config) *Error {

	/* we use a temporary config so we can bail out of any changes when validation
	fails further down the line
	*/

	tempConf := *c
	tempConf.Routes = []Route{}
	tempConf.Frontends = config.Frontends
	tempConf.Backends = config.Backends

	for _, route := range config.Routes {
		if err := tempConf.AddRoute(route); err != nil {
			return err
		}

	}

	c.Frontends = tempConf.Frontends
	c.Backends = tempConf.Backends
	c.Routes = tempConf.Routes

	return nil
}

// gets a frontend
func (c *Config) GetFrontend(name string) (*Frontend, *Error) {

	var result *Frontend

	for _, fe := range c.Frontends {
		if fe.Name == name {
			return fe, nil
		}
	}
	return result, &Error{404, errors.New("no frontend found")}
}

// adds a frontend
func (c *Config) AddFrontend(frontend *Frontend) *Error {

	if c.FrontendExists(frontend.Name) {
		return nil
	}

	c.Frontends = append(c.Frontends, frontend)
	return nil
}

// deletes a frontend
func (c *Config) DeleteFrontend(name string) *Error {

	for i, fe := range c.Frontends {
		if fe.Name == name {
			c.Frontends = append(c.Frontends[:i], c.Frontends[i+1:]...)
			return nil
		}
	}
	return nil
}

// get the filters from a frontend
func (c *Config) GetFilters(frontend string) []*Filter {

	var filters []*Filter

	for _, fe := range c.Frontends {
		if fe.Name == frontend {
			filters = fe.Filters

		}
	}
	return filters
}

// set the filter on a frontend
func (c *Config) AddFilter(frontend string, filter *Filter) error {

	for _, fe := range c.Frontends {
		if fe.Name == frontend {
			fe.Filters = append(fe.Filters, filter)
		}
	}
	return nil
}

// delete a Filter from a frontend
func (c *Config) DeleteFilter(frontendName string, filterName string) *Error {

	for _, fe := range c.Frontends {
		if fe.Name == frontendName {
			for i, filter := range fe.Filters {
				if filter.Name == filterName {
					fe.Filters = append(fe.Filters[:i], fe.Filters[i+1:]...)
					return nil
				}
			}
		}
	}
	return nil
}

// gets a backend
func (c *Config) GetBackend(backend string) (*Backend, *Error) {

	var result *Backend

	for _, be := range c.Backends {
		if be.Name == backend {
			return be, nil
		}
	}
	return result, &Error{404, errors.New("no backend found")}

}

// gets all backends
func (c *Config) GetBackends() []*Backend {
	return c.Backends
}

// adds a frontend
func (c *Config) AddBackend(backend *Backend) *Error {

	if _, err := Validate(backend); err != nil {
		return &Error{400, err}
	}

	if c.BackendExists(backend.Name) {
		return nil
	}

	c.Backends = append(c.Backends, backend)
	return nil

}

/* Deleting a backend is tricky. Frontends have a default backend. Removing that backend and then reloading
the configuration will crash Haproxy. This means some extra protection is put into this method to check
if this backend is still used. If not, it can be deleted.
*/
func (c *Config) DeleteBackend(name string) *Error {

	if err := c.BackendUsed(name); err != nil {
		return err
	}

	for i, be := range c.Backends {
		if be.Name == name {
			c.Backends = append(c.Backends[:i], c.Backends[i+1:]...)
			return nil
		}
	}
	return nil
}

// gets all servers of a specific backend
func (c *Config) GetServers(backendName string) ([]*ServerDetail, *Error) {

	var result []*ServerDetail

	for _, be := range c.Backends {
		if be.Name == backendName {
			return be.Servers, nil
		}
	}
	return result, &Error{404, errors.New("no servers found")}
}

func (c *Config) GetServer(backendName string, serverName string) (*ServerDetail, *Error) {

	var result *ServerDetail

	for _, be := range c.Backends {
		if be.Name == backendName {
			for _, srv := range be.Servers {
				if srv.Name == serverName {
					return srv, nil
				}
			}
		}
	}
	return result, &Error{404, errors.New("no server found")}
}

// adds a Server
func (c *Config) AddServer(backendName string, server *ServerDetail) *Error {

	if _, err := Validate(server); err != nil {
		return &Error{400, err}
	}

	for _, be := range c.Backends {
		if be.Name == backendName {
			be.Servers = append(be.Servers, server)
			return nil
		}
	}
	return &Error{404, errors.New("No backend found")}
}

func (c *Config) DeleteServer(backendName string, serverName string) *Error {
	for _, be := range c.Backends {
		if be.Name == backendName {
			for i, srv := range be.Servers {
				if srv.Name == serverName {
					be.Servers = append(be.Servers[:i], be.Servers[i+1:]...)
					return nil
				}
			}
		}
	}
	return nil
}

// Render a config object to a HAproxy config file
func (c *Config) Render() error {

	// read the template
	f, err := ioutil.ReadFile(c.TemplateFile)
	if err != nil {
		return err
	}

	// create a file for the config
	fp, err := os.OpenFile(c.ConfigFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer fp.Close()

	// render the template
	t := template.Must(template.New(c.TemplateFile).Parse(string(f)))
	err = t.Execute(fp, &c)
	if err != nil {
		return err
	}

	return nil
}

// save the JSON config to disk
func (c *Config) Persist() error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(c.JsonFile, b, 0666)
	if err != nil {
		return err
	}
	return nil
}

func (c *Config) RenderAndPersist() error {

	err := c.Render()
	if err != nil {
		return err
	}

	err = c.Persist()
	if err != nil {
		return err
	}

	return nil
}

// helper function to check if a Frontend exists
func (c *Config) FrontendExists(name string) bool {

	for _, frontend := range c.Frontends {
		if frontend.Name == name {
			return true
		}
	}
	return false
}

// helper function to check if a Backend exists
func (c *Config) BackendExists(name string) bool {

	for _, backend := range c.Backends {
		if backend.Name == name {
			return true
		}
	}
	return false
}

/*	Helper function to check if a Backend is used by a Frontend as a default backend or a filter destination
 */
func (c *Config) BackendUsed(name string) *Error {

	if c.BackendExists(name) {
		for _, frontend := range c.Frontends {
			if frontend.DefaultBackend == name {
				return &Error{400, errors.New("Backend still in use by: " + frontend.Name)}
			}
			for _, filter := range frontend.Filters {
				if filter.Destination == name {
					return &Error{400, errors.New("Backend still in use by: " + frontend.Name + ".Filters." + filter.Name)}
				}
			}
		}

	}
	return nil
}

// helper function to check if a Route exists
func (c *Config) RouteExists(name string) bool {

	for _, route := range c.Routes {
		if route.Name == name {
			return true
		}
	}
	return false
}

// helper function to check if a Service exists
func (c *Config) ServiceExists(routeName string, serviceName string) bool {

	for _, rt := range c.Routes {
		if rt.Name == routeName {
			for _, grp := range rt.Services {
				if grp.Name == serviceName {
					return true
				}
			}
		}
	}
	return false
}

// helper function to check if a Server exists in a specific Service
func (c *Config) ServerExists(routeName string, serviceName string, serverName string) bool {

	for _, rt := range c.Routes {
		if rt.Name == routeName {
			for _, grp := range rt.Services {
				if grp.Name == serviceName {
					for _, server := range grp.Servers {
						if server.Name == serverName {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// helper function to create a Backend or Frontend name based on a Route and Service
func ServiceName(routeName string, serviceName string) string {
	return routeName + SEPARATOR + serviceName
}
func RouteName(routeName string, serviceName string) string {
	return routeName + SEPARATOR + serviceName
}

func BackendName(routeName string, serviceName string) string {
	return routeName + SEPARATOR + serviceName
}

func FrontendName(routeName string, serviceName string) string {
	return routeName + SEPARATOR + serviceName
}

func ServerName(routeName string, serviceName string) string {
	return routeName + SEPARATOR + serviceName
}

func FilterName(routeName string, filterDestination string) string {
	return routeName + SEPARATOR + filterDestination
}
