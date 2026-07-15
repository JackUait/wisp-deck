// Package terminalcontrol owns the bounded streaming policy applied between a
// managed agent PTY and the outer terminal.
package terminalcontrol

import "github.com/jackuait/wisp-deck/internal/codexadapter"

// MaxRetainedBytes is the hard bound for one candidate notification payload.
const MaxRetainedBytes = codexadapter.MaxOSC9RetainedBytes

// Event is one complete OSC 9 notification removed from terminal output.
// Callers that do not use semantic terminal notifications may ignore it.
type Event = codexadapter.OSC9Event

// Filter removes terminal notification controls while preserving unrelated
// terminal bytes. The zero value is ready for use.
type Filter = codexadapter.OSC9Filter
