package printers

import "errors"

var ErrUnavailable = errors.New("printer backend unavailable")

// A queue state is CUPS' reported state, not proof of physical printer readiness.
type Printer struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	Model               string       `json:"model"`
	Location            string       `json:"location"`
	State               string       `json:"state"`
	AcceptingJobs       *bool        `json:"acceptingJobs"`
	StateReasons        []string     `json:"stateReasons"`
	StateMessage        string       `json:"stateMessage,omitempty"`
	DeviceState         string       `json:"deviceState,omitempty"`
	Capabilities        Capabilities `json:"capabilities"`
	ManualDuplexProfile string       `json:"manualDuplexProfile,omitempty"`
	Activity            *Activity    `json:"activity,omitempty"`
}

// Activity describes PrintBridge's reservation without revealing task identities.
// External CUPS clients are represented by the separate queue state.
type Activity struct {
	State       string `json:"state"`
	WaitingJobs int    `json:"waitingJobs"`
}

type Capabilities struct {
	Media              []string   `json:"media"`
	Sides              []string   `json:"sides"`
	ColorModes         []string   `json:"colorModes"`
	Copies             *CopyRange `json:"copies"`
	DefaultMedia       string     `json:"defaultMedia"`
	DefaultSides       string     `json:"defaultSides"`
	DefaultColorMode   string     `json:"defaultColorMode"`
	MediaTypes         []string   `json:"mediaTypes,omitempty"`
	MediaSources       []string   `json:"mediaSources,omitempty"`
	DefaultMediaType   string     `json:"defaultMediaType,omitempty"`
	DefaultMediaSource string     `json:"defaultMediaSource,omitempty"`
}

type CopyRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}
