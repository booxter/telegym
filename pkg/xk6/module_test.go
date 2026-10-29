package telegym

import (
	"testing"

	"go.k6.io/k6/v2/lib"
)

// TestModuleExportsAPI walks the path k6 takes at runtime: RootModule ->
// per-VU instance -> default export. The JS side only ever sees *API, so a
// wrong type here breaks every scenario at import time.
func TestModuleExportsAPI(t *testing.T) {
	rm := New()
	if rm.client == nil {
		t.Fatal("New() left the shared client nil")
	}

	vu := fakeVU{state: &lib.State{VUID: 3}}
	inst := rm.NewModuleInstance(vu)
	api, ok := inst.Exports().Default.(*API)
	if !ok {
		t.Fatalf("default export is %T, want *API", inst.Exports().Default)
	}
	if api.client != rm.client {
		t.Error("API does not share the RootModule client; VUs would not reuse connections")
	}

	u := api.NewUser(0)
	if u.vu != vu {
		t.Error("API.NewUser did not bind the user to the instance VU")
	}
	// VUID 3 lands in the high part of the auto-allocated ID.
	if u.ChatID/1_000_000 != 3 {
		t.Errorf("ChatID = %d, want it allocated in the VU 3 range", u.ChatID)
	}
}

// TestNewClientReadsEnv covers the non-default branch of envOr: scenarios in
// CI point the extension at a mock on another host via env vars.
func TestNewClientReadsEnv(t *testing.T) {
	t.Setenv("TELEGYM_MOCK_URL", "http://mock:9999")
	t.Setenv("TELEGYM_BOT_TOKEN", "1:abc")

	c := NewClient()
	if c.mockURL != "http://mock:9999" {
		t.Errorf("mockURL = %q, want http://mock:9999", c.mockURL)
	}
	if c.token != "1:abc" {
		t.Errorf("token = %q, want 1:abc", c.token)
	}
}

// TestAllocateIDSeparatesVUs guards the ID layout: two VUs allocating at
// the same time must never hand out the same chat_id.
func TestAllocateIDSeparatesVUs(t *testing.T) {
	c := NewClient()
	a := c.allocateID(fakeVU{state: &lib.State{VUID: 1}})
	b := c.allocateID(fakeVU{state: &lib.State{VUID: 2}})
	if a/1_000_000 != 1 || b/1_000_000 != 2 {
		t.Errorf("IDs %d and %d are not in their VU ranges", a, b)
	}

	// A VU without state (init context) falls back to the VU 1 range.
	if id := c.allocateID(fakeVU{}); id/1_000_000 != 1 {
		t.Errorf("stateless VU got ID %d, want VU 1 range", id)
	}
}
