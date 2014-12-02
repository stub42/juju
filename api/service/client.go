// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package service provides access to the service api facade.
// This facade contains api calls that are specific to services.
// As a rule of thumb, if the argument for an api requries a service name
// and affects only that service then the call belongs here.
package service

import (
	"github.com/juju/errors"

	"github.com/juju/juju/api"
	"github.com/juju/juju/api/base"
	"github.com/juju/juju/apiserver/params"
)

// Client provides access to the service api
type Client struct {
	base.ClientFacade
	st     *api.State
	facade base.FacadeCaller
}

// ServiceClient defines the methods on the service API end point.
type ServiceClient interface {
	SetServiceMetricCredentials(map[string][]byte) error
}

var _ ServiceClient = (*Client)(nil)

// NewClient creates a new client for accessing the service api
func NewClient(st *api.State) *Client {
	frontend, backend := base.NewClientFacade(st, "Service")
	return &Client{ClientFacade: frontend, st: st, facade: backend}
}

// SetServiceMetricCredentials sets the metric credentials for the services specified.
func (c *Client) SetServiceMetricCredentials(args map[string][]byte) error {
	var creds []params.ServiceMetricCredential
	for k, v := range args {
		creds = append(creds, params.ServiceMetricCredential{k, v})
	}
	p := params.ServiceMetricCredentials{creds}
	results := new(params.ErrorResults)
	err := c.facade.FacadeCall("SetServiceMetricCredentials", p, results)
	if err != nil {
		return errors.Trace(err)
	}
	return results.OneError()
}
