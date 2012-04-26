package cmd_test

import (
	"fmt"
	"launchpad.net/gnuflag"
	. "launchpad.net/gocheck"
	cmd "launchpad.net/juju/go/cmd"
	"launchpad.net/juju/go/log"
	"os"
	"path/filepath"
	"testing"
)

func Test(t *testing.T) { TestingT(t) }

type TestCommand struct {
	Name  string
	Value string
}

func (c *TestCommand) Info() *cmd.Info {
	return &cmd.Info{c.Name, "<something>", c.Name + " the juju", c.Name + " doc"}
}

func (c *TestCommand) Init(f *gnuflag.FlagSet, args []string) error {
	f.StringVar(&c.Value, "value", "", "doc")
	if err := f.Parse(true, args); err != nil {
		return err
	}
	return cmd.CheckEmpty(f.Args())
}

func (c *TestCommand) Run(ctx *cmd.Context) error {
	return fmt.Errorf("BORKEN: value is %s.", c.Value)
}

func initCmd(c cmd.Command, args []string) error {
	return c.Init(gnuflag.NewFlagSet("", gnuflag.ContinueOnError), args)
}

func initEmpty(args []string) (*cmd.SuperCommand, error) {
	jc := cmd.NewSuperCommand("jujutest", "", "")
	return jc, initCmd(jc, args)
}

func initDefenestrate(args []string) (*cmd.SuperCommand, *TestCommand, error) {
	jc := cmd.NewSuperCommand("jujutest", "", "")
	tc := &TestCommand{Name: "defenestrate"}
	jc.Register(tc)
	return jc, tc, initCmd(jc, args)
}

type CommandSuite struct{}

var _ = Suite(&CommandSuite{})

func (s *CommandSuite) TestSubcommandDispatch(c *C) {
	jc, err := initEmpty([]string{})
	c.Assert(err, ErrorMatches, `no command specified`)
	info := jc.Info()
	c.Assert(info.Name, Equals, "jujutest")
	c.Assert(info.Args, Equals, "<command> ...")
	c.Assert(info.Doc, Equals, "")

	jc, _, err = initDefenestrate([]string{"discombobulate"})
	c.Assert(err, ErrorMatches, "unrecognised command: jujutest discombobulate")
	info = jc.Info()
	c.Assert(info.Name, Equals, "jujutest")
	c.Assert(info.Args, Equals, "<command> ...")
	c.Assert(info.Doc, Equals, "commands:\n defenestrate - defenestrate the juju")

	jc, tc, err := initDefenestrate([]string{"defenestrate"})
	c.Assert(err, IsNil)
	c.Assert(tc.Value, Equals, "")
	info = jc.Info()
	c.Assert(info.Name, Equals, "jujutest defenestrate")
	c.Assert(info.Args, Equals, "<something>")
	c.Assert(info.Doc, Equals, "defenestrate doc")

	_, tc, err = initDefenestrate([]string{"defenestrate", "--value", "firmly"})
	c.Assert(err, IsNil)
	c.Assert(tc.Value, Equals, "firmly")

	_, tc, err = initDefenestrate([]string{"defenestrate", "gibberish"})
	c.Assert(err, ErrorMatches, `unrecognised args: \[gibberish\]`)
}

func (s *CommandSuite) TestRegister(c *C) {
	jc := cmd.NewSuperCommand("jujutest", "to be purposeful", "doc\nblah\ndoc")
	jc.Register(&TestCommand{Name: "flip"})
	jc.Register(&TestCommand{Name: "flapbabble"})

	badCall := func() { jc.Register(&TestCommand{Name: "flip"}) }
	c.Assert(badCall, PanicMatches, "command already registered: flip")
	info := jc.Info()
	c.Assert(info.Name, Equals, "jujutest")
	c.Assert(info.Purpose, Equals, "to be purposeful")
	c.Assert(info.Doc, Equals, `doc
blah
doc

commands:
 flapbabble - flapbabble the juju
 flip       - flip the juju`)
}

func (s *CommandSuite) TestDebug(c *C) {
	jc, err := initEmpty([]string{})
	c.Assert(err, ErrorMatches, "no command specified")
	c.Assert(jc.Debug, Equals, false)

	jc, _, err = initDefenestrate([]string{"defenestrate"})
	c.Assert(err, IsNil)
	c.Assert(jc.Debug, Equals, false)

	jc, err = initEmpty([]string{"--debug"})
	c.Assert(err, ErrorMatches, "no command specified")
	c.Assert(jc.Debug, Equals, true)

	jc, _, err = initDefenestrate([]string{"--debug", "defenestrate"})
	c.Assert(err, IsNil)
	c.Assert(jc.Debug, Equals, true)
}

func (s *CommandSuite) TestVerbose(c *C) {
	jc, err := initEmpty([]string{})
	c.Assert(err, ErrorMatches, "no command specified")
	c.Assert(jc.Verbose, Equals, false)

	jc, _, err = initDefenestrate([]string{"defenestrate"})
	c.Assert(err, IsNil)
	c.Assert(jc.Verbose, Equals, false)

	jc, err = initEmpty([]string{"--verbose"})
	c.Assert(err, ErrorMatches, "no command specified")
	c.Assert(jc.Verbose, Equals, true)

	jc, _, err = initDefenestrate([]string{"-v", "defenestrate"})
	c.Assert(err, IsNil)
	c.Assert(jc.Verbose, Equals, true)
}

func (s *CommandSuite) TestLogFile(c *C) {
	jc, err := initEmpty([]string{})
	c.Assert(err, ErrorMatches, "no command specified")
	c.Assert(jc.LogFile, Equals, "")

	jc, _, err = initDefenestrate([]string{"defenestrate"})
	c.Assert(err, IsNil)
	c.Assert(jc.LogFile, Equals, "")

	jc, err = initEmpty([]string{"--log-file", "foo"})
	c.Assert(err, ErrorMatches, "no command specified")
	c.Assert(jc.LogFile, Equals, "foo")

	jc, _, err = initDefenestrate([]string{"--log-file", "bar", "defenestrate"})
	c.Assert(err, IsNil)
	c.Assert(jc.LogFile, Equals, "bar")
}

func saveLog() func() {
	target, debug := log.Target, log.Debug
	return func() {
		log.Target, log.Debug = target, debug
	}
}

func checkRun(c *C, args []string, debug bool, target Checker, logfile string) {
	defer saveLog()()
	args = append([]string{"defenestrate", "--value", "cheese"}, args...)
	jc, _, err := initDefenestrate(args)
	c.Assert(err, IsNil)

	err = jc.Run(cmd.DefaultContext())
	c.Assert(err, ErrorMatches, "BORKEN: value is cheese.")

	c.Assert(log.Debug, Equals, debug)
	c.Assert(log.Target, target)
	if logfile != "" {
		_, err := os.Stat(logfile)
		c.Assert(err, IsNil)
	}
}

func (s *CommandSuite) TestRun(c *C) {
	checkRun(c, []string{}, false, IsNil, "")
	checkRun(c, []string{"--debug"}, true, NotNil, "")
	checkRun(c, []string{"--verbose"}, false, NotNil, "")
	checkRun(c, []string{"--verbose", "--debug"}, true, NotNil, "")

	tmp := c.MkDir()
	path := filepath.Join(tmp, "log-1")
	checkRun(c, []string{"--log-file", path}, false, NotNil, path)

	path = filepath.Join(tmp, "log-2")
	checkRun(c, []string{"--log-file", path, "--debug"}, true, NotNil, path)

	path = filepath.Join(tmp, "log-3")
	checkRun(c, []string{"--log-file", path, "--verbose"}, false, NotNil, path)

	path = filepath.Join(tmp, "log-4")
	checkRun(c, []string{"--log-file", path, "--verbose", "--debug"}, true, NotNil, path)
}
