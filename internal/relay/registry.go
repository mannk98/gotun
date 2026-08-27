// Package relay is the proxyd server: it accepts tunnel connections, authenticates
// them, and for each registered service opens a public TCP listener that splices
// visitors back over a yamux stream.
package relay

import (
	"net"
	"sync"

	"github.com/hashicorp/yamux"
)

type ClientReg struct {
	ID        string
	Session   *yamux.Session
	Services  map[string]string // service name -> local addr on the client
	listeners []net.Listener
}

func (c *ClientReg) AddListener(ln net.Listener) { c.listeners = append(c.listeners, ln) }

func (c *ClientReg) Close() {
	for _, ln := range c.listeners {
		ln.Close()
	}
	if c.Session != nil {
		c.Session.Close()
	}
}

type Registry struct {
	mu      sync.Mutex
	clients map[string]*ClientReg
}

func NewRegistry() *Registry { return &Registry{clients: map[string]*ClientReg{}} }

func (r *Registry) Put(c *ClientReg) (replaced *ClientReg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	replaced = r.clients[c.ID]
	r.clients[c.ID] = c
	return replaced
}

// Delete removes the registry entry for id only if it still holds expect —
// a compare-and-delete so a stale client's cleanup can never clobber a
// newer registration that replaced it (same-ID rehello race).
func (r *Registry) Delete(id string, expect *ClientReg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients[id] == expect {
		delete(r.clients, id)
	}
}
