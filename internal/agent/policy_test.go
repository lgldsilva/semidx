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

func TestParseActionPolicy(t *testing.T) {
	cases := []struct {
		in      string
		wantPol ActionPolicy
		wantOK  bool
	}{
		{"propose", PolicyPropose, true},
		{"confirm", PolicyConfirm, true},
		{"execute", PolicyExecute, true},
		{"off", PolicyPropose, false},     // explicitly disabled → not enabled
		{"", PolicyPropose, false},        // unset → not enabled
		{"PROPOSE", PolicyPropose, false}, // case-sensitive: callers normalize to lowercase first
		{"garbage", PolicyPropose, false},
	}
	for _, c := range cases {
		gotPol, gotOK := ParseActionPolicy(c.in)
		if gotOK != c.wantOK {
			t.Errorf("ParseActionPolicy(%q) ok = %v, want %v", c.in, gotOK, c.wantOK)
		}
		if gotOK && gotPol != c.wantPol {
			t.Errorf("ParseActionPolicy(%q) policy = %v, want %v", c.in, gotPol, c.wantPol)
		}
	}
}
