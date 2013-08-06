// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package common_test

import (
	"fmt"

	gc "launchpad.net/gocheck"

	"launchpad.net/juju-core/state"
	"launchpad.net/juju-core/state/api/params"
	"launchpad.net/juju-core/state/apiserver/common"
	apiservertesting "launchpad.net/juju-core/state/apiserver/testing"
)

type removeSuite struct{}

var _ = gc.Suite(&removeSuite{})

type fakeRemover struct {
	state.Entity
	life          state.Life
	errEnsureDead error
	errRemove     error
}

func (r *fakeRemover) EnsureDead() error {
	return r.errEnsureDead
}

func (r *fakeRemover) Remove() error {
	return r.errRemove
}

func (r *fakeRemover) Life() state.Life {
	return r.life
}

func (*removeSuite) TestRemove(c *gc.C) {
	st := &fakeState{
		entities: map[string]state.Entity{
			"x0": &fakeRemover{life: state.Dying, errEnsureDead: fmt.Errorf("x0 EnsureDead fails")},
			"x1": &fakeRemover{life: state.Dying, errRemove: fmt.Errorf("x1 Remove fails")},
			"x2": &fakeRemover{life: state.Alive},
			"x3": &fakeRemover{life: state.Dying},
			"x4": &fakeRemover{life: state.Dead},
		},
	}
	getCanModify := func() (common.AuthFunc, error) {
		return func(tag string) bool {
			switch tag {
			case "x0", "x1", "x2", "x3":
				return true
			}
			return false
		}, nil
	}
	r := common.NewRemover(st, getCanModify)
	entities := params.Entities{[]params.Entity{
		{"x0"}, {"x1"}, {"x2"}, {"x3"}, {"x4"}, {"x6"},
	}}
	result, err := r.Remove(entities)
	c.Assert(err, gc.IsNil)
	c.Assert(result, gc.DeepEquals, params.ErrorResults{
		Results: []params.ErrorResult{
			{&params.Error{Message: "x0 EnsureDead fails"}},
			{&params.Error{Message: "x1 Remove fails"}},
			{&params.Error{Message: `cannot remove entity "x2": still alive`}},
			{nil},
			{apiservertesting.ErrUnauthorized},
			{apiservertesting.ErrUnauthorized},
		},
	})
}

func (*removeSuite) TestRemoveError(c *gc.C) {
	getCanModify := func() (common.AuthFunc, error) {
		return nil, fmt.Errorf("pow")
	}
	r := common.NewRemover(&fakeState{}, getCanModify)
	_, err := r.Remove(params.Entities{[]params.Entity{{"x0"}}})
	c.Assert(err, gc.ErrorMatches, "pow")
}

func (*removeSuite) TestRemoveNoArgsNoError(c *gc.C) {
	getCanModify := func() (common.AuthFunc, error) {
		return nil, fmt.Errorf("pow")
	}
	r := common.NewRemover(&fakeState{}, getCanModify)
	result, err := r.Remove(params.Entities{})
	c.Assert(err, gc.IsNil)
	c.Assert(result.Results, gc.HasLen, 0)
}
