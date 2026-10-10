package peers

import (
	"fmt"
	"strings"
)

// Policy decides what happens when a peer offers us files.
type Policy int

const (
	PolicyAsk        Policy = iota // prompt the user to accept or decline
	PolicyAutoAccept               // save without asking
	PolicyBlock                    // reject everything
)

func (p Policy) String() string {
	switch p {
	case PolicyAsk:
		return "ask"
	case PolicyAutoAccept:
		return "auto"
	case PolicyBlock:
		return "block"
	}
	return fmt.Sprintf("Policy(%d)", int(p))
}

func ParsePolicy(s string) (Policy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ask", "manual":
		return PolicyAsk, nil
	case "auto", "auto-accept", "autoaccept":
		return PolicyAutoAccept, nil
	case "block", "blocked":
		return PolicyBlock, nil
	}
	return 0, fmt.Errorf("unknown policy %q (want ask, auto or block)", s)
}

func (p Policy) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

func (p *Policy) UnmarshalText(b []byte) error {
	v, err := ParsePolicy(string(b))
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// Decision is the outcome of applying a peer's policy to an incoming offer.
type Decision int

const (
	DecisionAsk Decision = iota
	DecisionAccept
	DecisionReject
)

// Decide applies the peer's policy to an offer of totalSize bytes.
func (p Peer) Decide(totalSize int64) Decision {
	switch p.Policy {
	case PolicyBlock:
		return DecisionReject
	case PolicyAutoAccept:
		if p.MaxAutoSize > 0 && totalSize > p.MaxAutoSize {
			return DecisionAsk
		}
		return DecisionAccept
	}
	return DecisionAsk
}
