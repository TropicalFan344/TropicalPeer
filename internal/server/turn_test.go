package server

import (
	"net"
	"strings"
	"testing"

	"github.com/pion/turn/v5"
)

// A client with the credentials ICE hands out gets a relay allocation.
func TestTURNAllocates(t *testing.T) {
	tn, err := StartTURN(TURNConfig{Port: 34780, Host: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), RelayMin: 34790, RelayMax: 34799, AllowLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Close()
	ice := tn.ICE("session1")
	creds := ice[1]

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	addr := strings.TrimPrefix(strings.Split(creds.URLs[0], "?")[0], "turn:")
	c, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: addr, TURNServerAddr: addr, Conn: conn,
		Username: creds.Username, Password: creds.Credential, Realm: "tropicalpeer",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	relay, err := c.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	defer relay.Close()
	t.Logf("relay %v", relay.LocalAddr())
}
