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
