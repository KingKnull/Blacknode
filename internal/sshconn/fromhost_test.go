package sshconn

import (
	"testing"

	"github.com/blacknode/blacknode/internal/store"
)

// TestFromHostDefaultsUnsetPort locks the connection-side half of group port
// inheritance: a host stored with an unset port (0) dials on 22, while an
// explicit port is preserved. store.Hosts.Create stores 0 as "unset" so the
// group default can apply; the 22 fallback must therefore live here.
func TestFromHostDefaultsUnsetPort(t *testing.T) {
	if got := FromHost(store.Host{Host: "h", Username: "u"}).Port; got != 22 {
		t.Errorf("unset port → %d, want 22", got)
	}
	if got := FromHost(store.Host{Host: "h", Username: "u", Port: 2222}).Port; got != 2222 {
		t.Errorf("explicit port → %d, want 2222", got)
	}
}
