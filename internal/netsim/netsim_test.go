package netsim

import (
	"net"
	"testing"
	"time"
)

func recv(t *testing.T, c *Conn) (string, net.Addr, bool) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buf := make([]byte, 100)
	n, from, err := c.ReadFrom(buf)
	if err != nil {
		return "", nil, false
	}
	return string(buf[:n]), from, true
}

func TestFilteringRules(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		// can a different port on the same server reach the inside host?
		otherPort bool
		// does a second destination reuse the same public port?
		samePublic bool
	}{
		{FullCone, true, true},
		{AddressRestricted, true, true},
		{PortRestricted, false, true},
		{Symmetric, false, false},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			n := New()
			srvA, _ := n.ListenPublic("1.0.0.1:1000")
			srvB, _ := n.ListenPublic("1.0.0.1:2000")
			inside := n.NewNAT("2.0.0.1", tc.kind).Listen()

			inside.WriteTo([]byte("hi"), srvA.LocalAddr())
			_, fromA, ok := recv(t, srvA)
			if !ok {
				t.Fatal("outbound packet lost")
			}
			// Reply from the same server socket always passes.
			srvA.WriteTo([]byte("back"), fromA)
			if _, _, ok := recv(t, inside); !ok {
				t.Fatal("reply was filtered")
			}
			// Unsolicited packet from another port of the same IP.
			srvB.WriteTo([]byte("other"), fromA)
			if _, _, ok := recv(t, inside); ok != tc.otherPort {
				t.Fatalf("other-port delivery = %v, want %v", ok, tc.otherPort)
			}
			// Mapping behaviour.
			inside.WriteTo([]byte("hi"), srvB.LocalAddr())
			_, fromB, _ := recv(t, srvB)
			if same := fromA.String() == fromB.String(); same != tc.samePublic {
				t.Fatalf("same public mapping = %v, want %v (%s vs %s)", same, tc.samePublic, fromA, fromB)
			}
		})
	}
}
