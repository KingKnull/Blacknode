package sshconn

import "testing"

func TestShellEscape(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "'hello'"},
		{"", "''"},
		{"it's", `'it'\''s'`},
		{"hello world", "'hello world'"},
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		{"`id`", "'`id`'"},
		{"a;b&&c|d", "'a;b&&c|d'"},
		{"foo\nbar", "'foo\nbar'"},
		{"$HOME/.ssh", "'$HOME/.ssh'"},
		{"test'quote'here", `'test'\''quote'\''here'`},
	}
	for _, tc := range cases {
		got := ShellEscape(tc.in)
		if got != tc.want {
			t.Errorf("ShellEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCmd(t *testing.T) {
	cases := []struct {
		name   string
		format string
		args   []any
		want   string
	}{
		{
			name:   "string is escaped",
			format: "echo %s",
			args:   []any{"hello world"},
			want:   "echo 'hello world'",
		},
		{
			name:   "int is not escaped",
			format: "ping -c %d %s",
			args:   []any{4, "example.com"},
			want:   "ping -c 4 'example.com'",
		},
		{
			name:   "injection attempt is neutralized",
			format: "docker logs --tail %d %s 2>&1",
			args:   []any{100, "$(rm -rf /)"},
			want:   "docker logs --tail 100 '$(rm -rf /)' 2>&1",
		},
		{
			name:   "multiple strings",
			format: "kill -%s %d 2>&1",
			args:   []any{"TERM", 1234},
			want:   "kill -'TERM' 1234 2>&1",
		},
		{
			name:   "no args",
			format: "uptime",
			args:   nil,
			want:   "uptime",
		},
		{
			name:   "quotes in value",
			format: "echo %s",
			args:   []any{"it's"},
			want:   `echo 'it'\''s'`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Cmd(tc.format, tc.args...)
			if got != tc.want {
				t.Errorf("Cmd(%q, %v) = %q, want %q", tc.format, tc.args, got, tc.want)
			}
		})
	}
}
