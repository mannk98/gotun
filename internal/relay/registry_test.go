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
	r.Delete("x", c2)
}

// TestRegistryDeleteIsIdentityAware guards the same-ID rehello race: a stale
// client's cleanup goroutine calling Delete after it was already replaced by
// Put must not clobber the newer live entry.
func TestRegistryDeleteIsIdentityAware(t *testing.T) {
	r := NewRegistry()
	a := &ClientReg{ID: "x"}
	if replaced := r.Put(a); replaced != nil {
		t.Fatalf("first Put should not replace")
	}
	b := &ClientReg{ID: "x"}
	if replaced := r.Put(b); replaced != a {
		t.Fatalf("second Put must return the prior reg (a) to close")
	}

	// Stale cleanup for the replaced client "a" fires with its own identity.
	r.Delete("x", a)

	// "b" must still be the live entry: Put(c) should report it as replaced.
	c := &ClientReg{ID: "x"}
	if replaced := r.Put(c); replaced != b {
		t.Fatalf("stale Delete(x, a) clobbered live entry; Put(c) replaced=%v want=%v", replaced, b)
	}
}
