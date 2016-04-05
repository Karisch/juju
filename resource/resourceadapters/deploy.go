// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package resourceadapters

import (
	"strconv"

	"github.com/juju/errors"
	"gopkg.in/juju/charm.v6-unstable"
	charmresource "gopkg.in/juju/charm.v6-unstable/resource"
	csparams "gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon.v1"

	"github.com/juju/juju/api"
	"github.com/juju/juju/resource/api/client"
	"github.com/juju/juju/resource/cmd"
)

// DeployResources uploads the bytes for the given files to the server and
// creates pending resource metadata for the all resource mentioned in the
// metadata. It returns a map of resource name to pending resource IDs.
func DeployResources(serviceID string, cURL *charm.URL, channel csparams.Channel, csMac *macaroon.Macaroon, filesAndRevisions map[string]string, resources map[string]charmresource.Meta, conn api.Connection) (ids map[string]string, err error) {
	client, err := newAPIClient(conn)
	if err != nil {
		return nil, errors.Trace(err)
	}

	filenames := make(map[string]string)
	revisions := make(map[string]int)
	for name, val := range filesAndRevisions {
		rev, err := strconv.Atoi(val)
		if err != nil {
			filenames[name] = val
		} else {
			revisions[name] = rev
		}
	}

	ids, err = cmd.DeployResources(cmd.DeployResourcesArgs{
		ServiceID:          serviceID,
		CharmURL:           cURL,
		Channel:            channel,
		CharmStoreMacaroon: csMac,
		Filenames:          filenames,
		Revisions:          revisions,
		ResourcesMeta:      resources,
		Client:             &deployClient{client},
	})
	if err != nil {
		return nil, errors.Trace(err)
	}
	return ids, nil
}

type deployClient struct {
	*client.Client
}

// AddPendingResources adds pending metadata for store-based resources.
func (cl *deployClient) AddPendingResources(serviceID string, cURL *charm.URL, channel csparams.Channel, csMac *macaroon.Macaroon, resources []charmresource.Resource) ([]string, error) {
	return cl.Client.AddPendingResources(client.AddPendingResourcesArgs{
		ServiceID:          serviceID,
		CharmURL:           cURL,
		Channel:            channel,
		CharmStoreMacaroon: csMac,
		Resources:          resources,
	})
}
