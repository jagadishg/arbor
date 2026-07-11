package model

import "time"

const SchemaVersion = 1

type Workspace struct {
	Version      int               `yaml:"version"`
	Name         string            `yaml:"name"`
	DefaultEnv   string            `yaml:"defaultEnvironment,omitempty"`
	Variables    map[string]string `yaml:"variables,omitempty"`
	HTTP         HTTPOptions       `yaml:"http,omitempty"`
	Path         string            `yaml:"-"`
	Root         string            `yaml:"-"`
	Requests     []Request         `yaml:"-"`
	Environments []Environment     `yaml:"-"`
	Scenarios    []Scenario        `yaml:"-"`
}

type HTTPOptions struct {
	Timeout         string `yaml:"timeout,omitempty"`
	FollowRedirects *bool  `yaml:"followRedirects,omitempty"`
	InsecureTLS     bool   `yaml:"insecureTLS,omitempty"`
}

type Request struct {
	Version int               `yaml:"version"`
	Kind    string            `yaml:"kind"`
	Name    string            `yaml:"name"`
	ID      string            `yaml:"id,omitempty"`
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Query   map[string]string `yaml:"query,omitempty"`
	Body    any               `yaml:"body,omitempty"`
	Timeout string            `yaml:"timeout,omitempty"`
	Assert  []string          `yaml:"assert,omitempty"`
	Extract map[string]string `yaml:"extract,omitempty"`
	Path    string            `yaml:"-"`
}

type Environment struct {
	Version   int               `yaml:"version"`
	Kind      string            `yaml:"kind"`
	Name      string            `yaml:"name"`
	Variables map[string]string `yaml:"variables,omitempty"`
	Secrets   map[string]string `yaml:"secrets,omitempty"`
	Path      string            `yaml:"-"`
}

type Scenario struct {
	Version   int               `yaml:"version"`
	Kind      string            `yaml:"kind"`
	Name      string            `yaml:"name"`
	ID        string            `yaml:"id,omitempty"`
	Variables map[string]string `yaml:"variables,omitempty"`
	Steps     []ScenarioStep    `yaml:"steps"`
	Path      string            `yaml:"-"`
}

type ScenarioStep struct {
	Name    string            `yaml:"name,omitempty"`
	Request string            `yaml:"request"`
	Assert  []string          `yaml:"assert,omitempty"`
	Extract map[string]string `yaml:"extract,omitempty"`
}

type Response struct {
	Status     string
	StatusCode int
	Headers    map[string][]string
	Body       []byte
	Duration   time.Duration
	Size       int64
	URL        string
}

func (r Request) Ref() string {
	if r.ID != "" {
		return r.ID
	}
	return r.Name
}

func (s Scenario) Ref() string {
	if s.ID != "" {
		return s.ID
	}
	return s.Name
}

func (ws *Workspace) RequestByRef(ref string) (Request, bool) {
	for _, request := range ws.Requests {
		if request.Ref() == ref || request.Name == ref {
			return request, true
		}
	}
	return Request{}, false
}

func (ws *Workspace) EnvironmentByName(name string) (Environment, bool) {
	for _, environment := range ws.Environments {
		if environment.Name == name {
			return environment, true
		}
	}
	return Environment{}, false
}

func (ws *Workspace) ScenarioByRef(ref string) (Scenario, bool) {
	for _, scenario := range ws.Scenarios {
		if scenario.Ref() == ref || scenario.Name == ref {
			return scenario, true
		}
	}
	return Scenario{}, false
}
