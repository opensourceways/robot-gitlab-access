package main

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
)

type configuration struct {
	Config accessConfig `json:"access,omitempty"`
}

func (c *configuration) Validate() error {
	return c.Config.validate()
}

func (c *configuration) SetDefault() {}

type accessConfig struct {
	// Plugins is a list available plugins.
	Plugins []pluginConfig `json:"plugins,omitempty"`
}

func (a *accessConfig) validate() error {
	for i := range a.Plugins {
		if err := a.Plugins[i].validate(); err != nil {
			return err
		}
	}

	ps := make([]string, len(a.Plugins))
	for i := range a.Plugins {
		ps[i] = a.Plugins[i].Name
	}

	total := sets.NewString(ps...)

	if n := len(ps) - total.Len(); n != 0 {
		return fmt.Errorf("%d duplicate plugin names exist", n)
	}

	return nil
}

type eventsDemux map[string][]string

func updateDemux(p *pluginConfig, d eventsDemux) {
	endpoint := p.Endpoint

	for _, e := range p.Events {
		if es, ok := d[e]; ok {
			d[e] = append(es, endpoint)
		} else {
			d[e] = []string{endpoint}
		}
	}
}

func (a *accessConfig) getDemux() eventsDemux {
	v := make(eventsDemux)

	for i := range a.Plugins {
		updateDemux(&a.Plugins[i], v)
	}

	return v
}

type pluginConfig struct {
	// Name of the plugin.
	Name string `json:"name" required:"true"`

	// Endpoint is the location of the plugin.
	Endpoint string `json:"endpoint" required:"true"`

	// Events are the events that this plugin can handle and should be forward to it.
	// If no events are specified, everything is sent.
	Events []string `json:"events,omitempty"`
}

func (p *pluginConfig) validate() error {
	if p.Name == "" {
		return fmt.Errorf("missing name")
	}

	if p.Endpoint == "" {
		return fmt.Errorf("missing endpoint")
	}

	// TODO validate the value of p.Endpoint
	return nil
}
