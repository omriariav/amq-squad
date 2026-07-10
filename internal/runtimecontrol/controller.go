package runtimecontrol

import "strings"

type Identity struct {
	Backend    string
	Session    string
	WindowID   string
	WindowName string
	PaneID     string
	Target     string
}

type Liveness struct {
	PaneAlive bool
}

type Controller interface {
	Backend() string
	Capabilities(Identity, Liveness) Capabilities
}

type TmuxController struct{}

func (TmuxController) Backend() string {
	return BackendTmux
}

func (TmuxController) Capabilities(_ Identity, live Liveness) Capabilities {
	return TmuxCapabilities(live.PaneAlive)
}

type Registry struct {
	controllers map[string]Controller
}

func NewRegistry(controllers ...Controller) *Registry {
	r := &Registry{controllers: map[string]Controller{}}
	for _, c := range controllers {
		r.Register(c)
	}
	return r
}

func (r *Registry) Register(c Controller) {
	if r == nil || c == nil {
		return
	}
	backend := strings.TrimSpace(c.Backend())
	if backend == "" {
		return
	}
	if r.controllers == nil {
		r.controllers = map[string]Controller{}
	}
	r.controllers[backend] = c
}

func (r *Registry) Lookup(backend string) (Controller, bool) {
	if r == nil {
		return nil, false
	}
	c, ok := r.controllers[strings.TrimSpace(backend)]
	return c, ok
}

func DefaultRegistry() *Registry {
	return NewRegistry(TmuxController{})
}
