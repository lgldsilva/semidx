package agent

import "testing"

func TestActionPolicyString(t *testing.T) {
	tests := []struct {
		p    ActionPolicy
		name string
		want string
	}{
		{PolicyPropose, "PolicyPropose", "propose"},
		{PolicyConfirm, "PolicyConfirm", "confirm"},
		{PolicyExecute, "PolicyExecute", "execute"},
		{ActionPolicy(99), "unknown(99)", "unknown"},
		{ActionPolicy(-1), "unknown(-1)", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.String(); got != tt.want {
				t.Errorf("ActionPolicy(%d).String() = %q, want %q", tt.p, got, tt.want)
			}
		})
	}
}
