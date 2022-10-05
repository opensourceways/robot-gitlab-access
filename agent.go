package main

import (
	"sync"
	"time"

	"github.com/opensourceways/community-robot-lib/config"
	"github.com/opensourceways/community-robot-lib/utils"
	"github.com/sirupsen/logrus"
)

type demuxConfigAgent struct {
	agent *config.ConfigAgent

	mut     sync.RWMutex
	demux   eventsDemux
	version string
	t       utils.Timer
}

func (ca *demuxConfigAgent) load() {
	v, c := ca.agent.GetConfig()
	if ca.version == v {
		return
	}

	nc, ok := c.(*configuration)
	if !ok {
		logrus.Errorf("can't convert to configuration")
		return
	}

	if nc == nil {
		logrus.Error("empty pointer of configuration")
		return
	}

	m := nc.Config.getDemux()

	// this function runs in serially, and ca.version is accessed in it,
	// so, there is no need to update it under the lock.
	ca.version = v

	ca.mut.Lock()
	ca.demux = m
	ca.mut.Unlock()
}

func (ca *demuxConfigAgent) getEndpoints(event string) (v []string) {
	ca.mut.RLock()
	if ca.demux != nil {
		v = ca.demux[event]
	}
	ca.mut.RUnlock()

	return
}

func (ca *demuxConfigAgent) start() {
	ca.load()

	ca.t.Start(
		func() {
			ca.load()
		},
		1*time.Minute,
		1*time.Minute,
	)
}

func (ca *demuxConfigAgent) stop() {
	ca.t.Stop()
}
