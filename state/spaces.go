// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"github.com/juju/errors"
	"github.com/juju/names"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/txn"

	"github.com/juju/juju/network"
)

// Space represents the state of a juju network space.
type Space struct {
	st  *State
	doc spaceDoc
}

type spaceDoc struct {
	DocID      string `bson:"_id"`
	ModelUUID  string `bson:"model-uuid"`
	Life       Life   `bson:"life"`
	Name       string `bson:"name"`
	IsPublic   bool   `bson:"is-public"`
	ProviderId string `bson:"providerid,omitempty"`
}

// Life returns whether the space is Alive, Dying or Dead.
func (s *Space) Life() Life {
	return s.doc.Life
}

// ID returns the unique id for the space, for other entities to reference it
func (s *Space) ID() string {
	return s.doc.DocID
}

// String implements fmt.Stringer.
func (s *Space) String() string {
	return s.doc.Name
}

// Name returns the name of the Space.
func (s *Space) Name() string {
	return s.doc.Name
}

// ProviderId returns the provider id of the space. This will be the empty
// string except on substrates that directly support spaces.
func (s *Space) ProviderId() network.Id {
	return network.Id(s.doc.ProviderId)
}

// Subnets returns all the subnets associated with the Space.
func (s *Space) Subnets() (results []*Subnet, err error) {
	defer errors.DeferredAnnotatef(&err, "cannot fetch subnets")
	name := s.Name()

	subnetsCollection, closer := s.st.getCollection(subnetsC)
	defer closer()

	var doc subnetDoc
	iter := subnetsCollection.Find(bson.D{{"space-name", name}}).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		subnet := &Subnet{s.st, doc}
		results = append(results, subnet)
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// AddSpace creates and returns a new space.
func (st *State) AddSpace(name string, providerId network.Id, subnets []string, isPublic bool) (newSpace *Space, err error) {
	defer errors.DeferredAnnotatef(&err, "adding space %q", name)
	if !names.IsValidSpace(name) {
		return nil, errors.NewNotValid(nil, "invalid space name")
	}

	spaceID := st.docID(name)
	spaceDoc := spaceDoc{
		DocID:      spaceID,
		ModelUUID:  st.ModelUUID(),
		Life:       Alive,
		Name:       name,
		IsPublic:   isPublic,
		ProviderId: string(providerId),
	}
	newSpace = &Space{doc: spaceDoc, st: st}

	ops := []txn.Op{{
		C:      spacesC,
		Id:     spaceID,
		Assert: txn.DocMissing,
		Insert: spaceDoc,
	}}

	if providerId != "" {
		ops = append(ops, st.networkEntityGlobalKeyOp("space", providerId))
	}

	for _, subnetId := range subnets {
		// TODO:(mfoord) once we have refcounting for subnets we should
		// also assert that the refcount is zero as moving the space of a
		// subnet in use is not permitted.
		ops = append(ops, txn.Op{
			C:      subnetsC,
			Id:     st.docID(subnetId),
			Assert: txn.DocExists,
			Update: bson.D{{"$set", bson.D{{"space-name", name}}}},
		})
	}

	if err := st.runTransaction(ops); err == txn.ErrAborted {
		if _, err := st.Space(name); err == nil {
			return nil, errors.AlreadyExistsf("space %q", name)
		}
		for _, subnetId := range subnets {
			if _, err := st.Subnet(subnetId); errors.IsNotFound(err) {
				return nil, err
			}
		}
		if err := newSpace.Refresh(); err != nil {
			if errors.IsNotFound(err) {
				return nil, errors.Errorf("ProviderId %q not unique", providerId)
			}
			return nil, errors.Trace(err)
		}
		return nil, errors.Trace(err)
	} else if err != nil {
		return nil, err
	}
	return newSpace, nil
}

// Space returns a space from state that matches the provided name. An error
// is returned if the space doesn't exist or if there was a problem accessing
// its information.
func (st *State) Space(name string) (*Space, error) {
	spaces, closer := st.getCollection(spacesC)
	defer closer()

	var doc spaceDoc
	err := spaces.FindId(name).One(&doc)
	if err == mgo.ErrNotFound {
		return nil, errors.NotFoundf("space %q", name)
	}
	if err != nil {
		return nil, errors.Annotatef(err, "cannot get space %q", name)
	}
	return &Space{st, doc}, nil
}

// AllSpaces returns all spaces for the model.
func (st *State) AllSpaces() ([]*Space, error) {
	spacesCollection, closer := st.getCollection(spacesC)
	defer closer()

	docs := []spaceDoc{}
	err := spacesCollection.Find(nil).All(&docs)
	if err != nil {
		return nil, errors.Annotatef(err, "cannot get all spaces")
	}
	spaces := make([]*Space, len(docs))
	for i, doc := range docs {
		spaces[i] = &Space{st: st, doc: doc}
	}
	return spaces, nil
}

// EnsureDead sets the Life of the space to Dead, if it's Alive. If the space is
// already Dead, no error is returned. When the space is no longer Alive or
// already removed, errNotAlive is returned.
func (s *Space) EnsureDead() (err error) {
	defer errors.DeferredAnnotatef(&err, "cannot set space %q to dead", s)

	if s.doc.Life == Dead {
		return nil
	}

	ops := []txn.Op{{
		C:      spacesC,
		Id:     s.doc.DocID,
		Update: bson.D{{"$set", bson.D{{"life", Dead}}}},
		Assert: isAliveDoc,
	}}

	txnErr := s.st.runTransaction(ops)
	if txnErr == nil {
		s.doc.Life = Dead
		return nil
	}
	return onAbort(txnErr, errNotAlive)
}

// Remove removes a Dead space. If the space is not Dead or it is already
// removed, an error is returned.
func (s *Space) Remove() (err error) {
	defer errors.DeferredAnnotatef(&err, "cannot remove space %q", s)

	if s.doc.Life != Dead {
		return errors.New("space is not dead")
	}

	ops := []txn.Op{{
		C:      spacesC,
		Id:     s.doc.DocID,
		Remove: true,
		Assert: isDeadDoc,
	}}
	if s.ProviderId() != "" {
		ops = append(ops, s.st.networkEntityGlobalKeyRemoveOp("space", s.ProviderId()))
	}

	txnErr := s.st.runTransaction(ops)
	if txnErr == nil {
		return nil
	}
	return onAbort(txnErr, errors.New("not found or not dead"))
}

// Refresh: refreshes the contents of the Space from the underlying state. It
// returns an error that satisfies errors.IsNotFound if the Space has been
// removed.
func (s *Space) Refresh() error {
	spaces, closer := s.st.getCollection(spacesC)
	defer closer()

	var doc spaceDoc
	err := spaces.FindId(s.doc.DocID).One(&doc)
	if err == mgo.ErrNotFound {
		return errors.NotFoundf("space %q", s)
	} else if err != nil {
		return errors.Errorf("cannot refresh space %q: %v", s, err)
	}
	s.doc = doc
	return nil
}
