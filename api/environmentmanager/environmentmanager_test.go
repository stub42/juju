// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package environmentmanager_test

import (
	"github.com/juju/juju/feature"
	"github.com/juju/juju/testing/factory"
	"github.com/juju/names"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/api"
	"github.com/juju/juju/api/environmentmanager"
	"github.com/juju/juju/juju"
	jujutesting "github.com/juju/juju/juju/testing"
)

type environmentmanagerSuite struct {
	jujutesting.JujuConnSuite
}

var _ = gc.Suite(&environmentmanagerSuite{})

func (s *environmentmanagerSuite) SetUpTest(c *gc.C) {
	s.JujuConnSuite.SetUpTest(c)
}

func (s *environmentmanagerSuite) OpenAPI(c *gc.C) *environmentmanager.Client {
	conn, err := juju.NewAPIState(s.AdminUserTag(c), s.Environ, api.DialOpts{})
	c.Assert(err, jc.ErrorIsNil)
	s.AddCleanup(func(*gc.C) { conn.Close() })
	return environmentmanager.NewClient(conn)
}

func (s *environmentmanagerSuite) TestCreateEnvironmentBadUser(c *gc.C) {
	envManager := s.OpenAPI(c)
	_, err := envManager.CreateEnvironment("not a user", nil, nil)
	c.Assert(err, gc.ErrorMatches, `invalid owner name "not a user"`)
}

func (s *environmentmanagerSuite) TestCreateEnvironmentFeatureNotEnabled(c *gc.C) {
	envManager := s.OpenAPI(c)
	_, err := envManager.CreateEnvironment("owner", nil, nil)
	c.Assert(err, gc.ErrorMatches, `unknown object type "EnvironmentManager"`)
}

func (s *environmentmanagerSuite) TestCreateEnvironmentMissingConfig(c *gc.C) {
	s.SetFeatureFlags(feature.MESS)
	envManager := s.OpenAPI(c)
	_, err := envManager.CreateEnvironment("owner", nil, nil)
	c.Assert(err, gc.ErrorMatches, `name: expected string, got nothing`)
}

func (s *environmentmanagerSuite) TestCreateEnvironment(c *gc.C) {
	s.SetFeatureFlags(feature.MESS)
	envManager := s.OpenAPI(c)
	user := s.Factory.MakeUser(c, nil)
	owner := user.UserTag().Username()
	newEnv, err := envManager.CreateEnvironment(owner, nil, map[string]interface{}{
		"name":            "new-env",
		"authorized-keys": "ssh-key",
		// dummy needs state-server
		"state-server": false,
	})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(newEnv.Name, gc.Equals, "new-env")
	c.Assert(newEnv.OwnerTag, gc.Equals, user.Tag().String())
	c.Assert(utils.IsValidUUIDString(newEnv.UUID), jc.IsTrue)
}

func (s *environmentmanagerSuite) TestListEnvironmentsBadUser(c *gc.C) {
	envManager := s.OpenAPI(c)
	_, err := envManager.ListEnvironments("not a user")
	c.Assert(err, gc.ErrorMatches, `invalid user name "not a user"`)
}

func (s *environmentmanagerSuite) TestListEnvironments(c *gc.C) {
	s.SetFeatureFlags(feature.MESS)
	owner := names.NewUserTag("user@remote")
	s.Factory.MakeEnvironment(c, &factory.EnvParams{
		Name: "first", Owner: owner}).Close()
	s.Factory.MakeEnvironment(c, &factory.EnvParams{
		Name: "second", Owner: owner}).Close()

	envManager := s.OpenAPI(c)
	envs, err := envManager.ListEnvironments("user@remote")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(envs, gc.HasLen, 2)

	envNames := []string{envs[0].Name, envs[1].Name}
	c.Assert(envNames, jc.SameContents, []string{"first", "second"})
}
