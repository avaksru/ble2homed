package mqtt

import "testing"

func TestSanitizeBrokerURI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "credentials with password",
			in:   "tcp://homed:secret@127.0.0.1:1883",
			want: "tcp://127.0.0.1:1883",
		},
		{
			name: "credentials without password",
			in:   "tcp://homed@127.0.0.1:1883",
			want: "tcp://127.0.0.1:1883",
		},
		{
			name: "percent encoded password",
			in:   "tcp://homed:p%40ss@127.0.0.1:1883",
			want: "tcp://127.0.0.1:1883",
		},
		{
			name: "no credentials",
			in:   "tcp://127.0.0.1:1883",
			want: "tcp://127.0.0.1:1883",
		},
		{
			name: "invalid uri",
			in:   "tcp://%",
			want: "tcp://%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeBrokerURI(tt.in); got != tt.want {
				t.Fatalf("sanitizeBrokerURI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
