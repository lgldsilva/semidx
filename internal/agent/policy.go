package agent

// ActionPolicy controls whether action tools propose, confirm, or execute.
type ActionPolicy int

const (
	PolicyPropose ActionPolicy = iota // describe what would be done, don't execute
	PolicyConfirm                     // propose and ask for confirmation
	PolicyExecute                     // execute immediately
)

func (p ActionPolicy) String() string {
	switch p {
	case PolicyPropose:
		return "propose"
	case PolicyConfirm:
		return "confirm"
	case PolicyExecute:
		return "execute"
	default:
		return "unknown"
	}
}

// ParseActionPolicy maps a config string to an ActionPolicy. ok is false for
// "off", "" or any unrecognized value — meaning action tools should not be
// enabled at all on that surface (the safe default). This lets a non-interactive
// surface (MCP, admin) opt into "propose" (describe only) or "execute" (run)
// without wiring an interactive approver, which "confirm" would require.
func ParseActionPolicy(s string) (ActionPolicy, bool) {
	switch s {
	case "propose":
		return PolicyPropose, true
	case "confirm":
		return PolicyConfirm, true
	case "execute":
		return PolicyExecute, true
	default:
		return PolicyPropose, false
	}
}
