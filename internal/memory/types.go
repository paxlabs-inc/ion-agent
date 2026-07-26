// Package memory defines the durable Cortex memory taxonomy.
package memory

import "fmt"

// Type is one of the nine closed Cortex memory namespaces.
type Type string

const (
	Identity   Type = "0x01"
	Fact       Type = "0x02"
	Preference Type = "0x03"
	Belief     Type = "0x04"
	Event      Type = "0x05"
	Goal       Type = "0x06"
	Constraint Type = "0x07"
	Capability Type = "0x08"
	Pattern    Type = "0x09"
)

var taxonomy = [...]Type{
	Identity,
	Fact,
	Preference,
	Belief,
	Event,
	Goal,
	Constraint,
	Capability,
	Pattern,
}

// Types returns the complete closed taxonomy in wire-code order.
func Types() []Type {
	return append([]Type(nil), taxonomy[:]...)
}

// Valid reports whether the type belongs to the closed taxonomy.
func (memoryType Type) Valid() bool {
	switch memoryType {
	case Identity, Fact, Preference, Belief, Event, Goal, Constraint, Capability, Pattern:
		return true
	default:
		return false
	}
}

// Validate rejects invented namespaces.
func (memoryType Type) Validate() error {
	if !memoryType.Valid() {
		return fmt.Errorf("memory: invalid type %q", memoryType)
	}
	return nil
}
