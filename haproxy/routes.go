// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package haproxy

import (
	"errors"
)

const (
	DEFAULT_WEIGHT = 100
)

// gets all routes
func (c *Config) GetRoutes() []Route {
	return c.Routes
}

// gets a route
func (c *Config) GetRoute(name string) (Route, *Error) {

	var route Route

	for _, rt := range c.Routes {
		if rt.Name == name {
			return rt, nil
			break
		}
	}
	return route, &Error{404, errors.New("no route found")}
}

// add a route to the configuration
func (c *Config) AddRoute(route Route) *Error {

	if c.RouteExists(route.Name) {
		return nil
	}

	if valid, err := Validate(route); valid != true {
		return &Error{400, err}
	}

	// create some slices for all the stuff we are going to create. These are just holders so we can
	// iterate over them once we have created all the basic structures and add them to the configuration.
	feSlice := []*Frontend{}
	beSlice := []*Backend{}

	// When creating a new route, we have to create the stable frontends and backends.
	// 1. Check if the route exists
	// 2. Create stable Backend with empty server slice
	// 3. Create stable Frontend and add the stable Backend to it

	stableBackend := c.backendFactory(route.Name, route.Protocol, true, []*ServerDetail{})
	beSlice = append(beSlice, stableBackend)

	// 4. As an extra step, we need to replace the destination in any filters with the full backend name
	//    and parse the filter short codes to proper Haproxy ACL conditions.
	resolvedFilters, err := resolveFilters(route)
	if err != nil {
		return &Error{400, err}
	}

	stableFrontend := c.frontendFactory(route.Name, route.Protocol, route.Port, resolvedFilters, stableBackend)
	feSlice = append(feSlice, stableFrontend)
	/*

		for services

			1. Create socketServer
			2. Add it to the stable Backend
			3. Create Backend (with empty server slice)
			4. Create Frontend (set socket to the socketServer, add Backend)
	*/

	for _, service := range route.Services {

		socketServer := c.socketServerFactory(ServerName(route.Name, service.Name), service.Weight)
		stableBackend.Servers = append(stableBackend.Servers, socketServer)

		backend := c.backendFactory(BackendName(route.Name, service.Name), route.Protocol, false, []*ServerDetail{})
		beSlice = append(beSlice, backend)

		frontend := c.socketFrontendFactory(FrontendName(route.Name, service.Name), route.Protocol, socketServer.UnixSock, backend)
		feSlice = append(feSlice, frontend)

		/*
			for servers
				1. Create Server, with a default weight.
				2. Add Server to Backend Servers slice
		*/
		for _, server := range service.Servers {
			srv := c.serverFactory(server.Name, DEFAULT_WEIGHT, server.Host, server.Port)
			backend.Servers = append(backend.Servers, srv)
		}
	}

	for _, fe := range feSlice {
		c.Frontends = append(c.Frontends, fe)
	}

	for _, be := range beSlice {
		c.Backends = append(c.Backends, be)
	}

	c.Routes = append(c.Routes, route)
	return nil
}

// deletes a route, cascading down the structure and remove all underpinning
// frontends, backends and servers.
func (c *Config) DeleteRoute(name string) *Error {

	for i, route := range c.Routes {

		if route.Name == name {

			// first remove the single frontend, getting rid of filters and other pointers to backends
			c.DeleteFrontend(route.Name)

			// then remove all the frontends and backends related to the services
			for _, service := range route.Services {
				c.DeleteFrontend(FrontendName(route.Name, service.Name))
				c.DeleteBackend(BackendName(route.Name, service.Name))
			}

			// then remove the single backend
			c.DeleteBackend(route.Name)

			c.Routes = append(c.Routes[:i], c.Routes[i+1:]...)
			return nil
		}
	}
	return nil
}

// just a convenience functions for a delete and a create
func (c *Config) UpdateRoute(name string, route *Route) *Error {

	if err := c.DeleteRoute(name); err != nil {
		return &Error{err.Code, err}
	}

	if err := c.AddRoute(*route); err != nil {
		return &Error{err.Code, err}
	}
	return nil
}

func (c *Config) GetRouteServices(name string) ([]*Service, *Error) {

	var services []*Service

	for _, rt := range c.Routes {
		if rt.Name == name {
			return rt.Services, nil
		}
	}
	return services, &Error{404, errors.New("no services found")}
}

func (c *Config) GetRouteService(routeName string, serviceName string) (*Service, *Error) {

	var service *Service

	for _, rt := range c.Routes {
		if rt.Name == routeName {
			for _, srv := range rt.Services {
				if srv.Name == serviceName {
					return srv, nil
				}
			}
		}
	}
	return service, &Error{404, errors.New("no  service found")}
}

func (c *Config) AddRouteServices(routeName string, services []*Service) *Error {

	for _, service := range services {
		if c.ServiceExists(routeName, service.Name) {
			return nil
		}
	}

	for _, route := range c.Routes {
		if route.Name == routeName {

			for _, service := range services {
				socketServer := c.socketServerFactory(ServerName(routeName, service.Name), service.Weight)
				backend := c.backendFactory(BackendName(route.Name, service.Name), route.Protocol, false, []*ServerDetail{})
				frontend := c.socketFrontendFactory(FrontendName(route.Name, service.Name), route.Protocol, socketServer.UnixSock, backend)

				for _, server := range service.Servers {
					srv := c.serverFactory(server.Name, service.Weight, server.Host, server.Port)
					backend.Servers = append(backend.Servers, srv)
				}

				if err := c.AddBackend(backend); err != nil {
					return &Error{500, errors.New("something went wrong adding backend: " + backend.Name)}
				}

				if err := c.AddFrontend(frontend); err != nil {
					return &Error{500, errors.New("something went wrong adding frontend: " + frontend.Name)}
				}

				route.Services = append(route.Services, service)
			}
			return nil
		}
	}

	return &Error{404, errors.New("no  route found")}
}

func (c *Config) DeleteRouteService(routeName string, serviceName string) *Error {

	for _, rt := range c.Routes {
		if rt.Name == routeName {
			for j, srv := range rt.Services {
				if srv.Name == serviceName {

					// order is important here. Always delete frontends first because they hold references to
					// backends. Deleting a backend that is still referenced first will fail.
					if err := c.DeleteFrontend(FrontendName(routeName, serviceName)); err != nil {
						return &Error{500, errors.New("Something went wrong deleting frontend: " + FrontendName(routeName, serviceName))}
					}

					if err := c.DeleteBackend(BackendName(routeName, serviceName)); err != nil {
						return &Error{500, errors.New("Something went wrong deleting backend: " + BackendName(routeName, serviceName))}
					}

					rt.Services = append(rt.Services[:j], rt.Services[j+1:]...)
					return nil
				}
			}
		}
	}
	return nil
}

// just a convenience functions for a delete and a create
func (c *Config) UpdateRouteService(routeName string, serviceName string, service *Service) *Error {

	if err := c.DeleteRouteService(routeName, serviceName); err != nil {
		return err
	}

	services := []*Service{service}

	if err := c.AddRouteServices(routeName, services); err != nil {
		return err
	}
	return nil
}

func (c *Config) UpdateRouteServices(routeName string, services []*Service) *Error {

	for _, srv := range services {
		if err := c.DeleteRouteService(routeName, srv.Name); err != nil {
			return err
		}
	}

	if err := c.AddRouteServices(routeName, services); err != nil {
		return err
	}

	return nil
}

func (c *Config) GetServiceServers(routeName string, serviceName string) ([]*Server, *Error) {

	var servers []*Server

	for _, rt := range c.Routes {
		if rt.Name == routeName {
			for _, srv := range rt.Services {
				if srv.Name == serviceName {
					return srv.Servers, nil
				}
			}
		}
	}
	return servers, &Error{404, errors.New("no servers found")}
}

func (c *Config) GetServiceServer(routeName string, serviceName string, serverName string) (*Server, *Error) {

	var server *Server

	for _, rt := range c.Routes {
		if rt.Name == routeName {
			for _, svc := range rt.Services {
				if svc.Name == serviceName {
					for _, srv := range svc.Servers {
						if srv.Name == serverName {
							return srv, nil
						}
					}
				}
			}
		}
	}
	return server, &Error{404, errors.New("no server found")}
}

func (c *Config) DeleteServiceServer(routeName string, serviceName string, serverName string) *Error {

	for _, rt := range c.Routes {
		if rt.Name == routeName {
			for _, grp := range rt.Services {
				if grp.Name == serviceName {
					for i, srv := range grp.Servers {
						if srv.Name == serverName {
							if err := c.DeleteServer(BackendName(routeName, serviceName), serverName); err != nil {
								return &Error{500, err}
							}
							grp.Servers = append(grp.Servers[:i], grp.Servers[i+1:]...)
							return nil
						}
					}
				}
			}
		}
	}
	return nil
}

func (c *Config) AddServiceServer(routeName string, serviceName string, server *Server) *Error {

	if c.ServerExists(routeName, serviceName, server.Name) {
		return nil
	}

	for _, route := range c.Routes {
		if route.Name == routeName {
			for _, service := range route.Services {
				if service.Name == serviceName {
					srvDetail := c.serverFactory(server.Name, service.Weight, server.Host, server.Port)
					c.AddServer(BackendName(routeName, serviceName), srvDetail)
					service.Servers = append(service.Servers, server)
					return nil
				}
			}
		}
	}
	return &Error{404, errors.New("no service found")}
}

// just a convenience functions for a delete and a create
func (c *Config) UpdateServiceServer(routeName string, serviceName string, serverName string, server *Server) *Error {

	if err := c.DeleteServiceServer(routeName, serviceName, serverName); err != nil {
		return err
	}

	if err := c.AddServiceServer(routeName, serviceName, server); err != nil {
		return err
	}
	return nil
}
