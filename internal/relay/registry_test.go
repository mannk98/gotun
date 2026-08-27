package relay

import "testing"

func TestRegistryPutReplaces(t *testing.T) {
	r := NewRegistry()
	c1 := &ClientReg{ID: "x", Services: map[string]string{"novnc": "127.0.0.1:8080"}}
	if replaced := r.Put(c1); replaced != nil {
		t.Fatalf("first Put should not replace")
	}
	c2 := &ClientReg{ID: "x"}
	replaced := r.Put(c2)
	if replaced != c1 {
		t.Fatalf("second Put must return the prior reg to close")
	}
	r.Delete("x")
}
