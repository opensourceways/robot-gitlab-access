package main

import (
	"flag"
	"net/http"
	"os"
	"strconv"

	"github.com/opensourceways/community-robot-lib/config"
	"github.com/opensourceways/community-robot-lib/interrupts"
	"github.com/opensourceways/community-robot-lib/logrusutil"
	liboptions "github.com/opensourceways/community-robot-lib/options"
	"github.com/opensourceways/community-robot-lib/utils"
	"github.com/sirupsen/logrus"
)

type options struct {
	plugin      liboptions.ServiceOptions
	userAgent   string
	enableDebug bool
}

func (o *options) Validate() error {
	return o.plugin.Validate()
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options

	o.plugin.AddFlags(fs)

	fs.StringVar(
		&o.userAgent, "user_agent", "Robot-Gitlab-Hook-Delivery",
		"the value for header of User-Agent sent in the event request.",
	)

	fs.BoolVar(
		&o.enableDebug, "enable_debug", false,
		"whether to enable debug model.",
	)

	fs.Parse(args)
	return o
}

const component = "robot-gitlab-access"

func main() {
	logrusutil.ComponentInit(component)

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	if o.enableDebug {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debug("debug enabled.")
	}

	// load config
	configAgent := config.NewConfigAgent(func() config.Config {
		return new(configuration)
	})
	if err := configAgent.Start(o.plugin.ConfigFile); err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	defer configAgent.Stop()

	// agent
	agent := demuxConfigAgent{agent: &configAgent, t: utils.NewTimer()}
	agent.start()
	defer agent.stop()

	// start server
	d := newDispatcher(&agent, o.userAgent)

	defer interrupts.WaitForGracefulShutdown()

	interrupts.OnInterrupt(func() {
		d.wait()
	})

	// Return 200 on / for health checks.
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})

	// For /gitlab-access, handle a hook message normally.
	http.Handle("/gitlab-access", d)

	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.plugin.Port)}

	interrupts.ListenAndServe(httpServer, o.plugin.GracePeriod)
}
