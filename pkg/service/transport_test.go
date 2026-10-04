package service

import (
	"context"
	"os/user"
	"strings"
	"testing"
	"time"
)

// The service records who asked for each mutation from the connection
// itself, so a client cannot claim to be someone else. Here the client is the
// test, so the peer is the test's own user.
func TestAcceptedConnectionPeerIsTheConnectingUser(t *testing.T) {
	name := testPipeName(t)
	ln, err := listen(name)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	accepted := make(chan clientConn, 1)
	go func() {
		c, acceptErr := ln.Accept()
		if acceptErr != nil {
			t.Errorf("accept: %v", acceptErr)
		}
		accepted <- c
	}()
	client, err := dial(context.Background(), name, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	if server == nil {
		t.FailNow()
	}
	defer func() { _ = server.Close() }()
	_ = ln.Close()

	got, err := server.Peer()
	if err != nil {
		t.Fatalf("Peer: %v", err)
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got.Name, me.Username) || got.ID != me.Uid {
		t.Fatalf("Peer = %+v, want %s (%s)", got, me.Username, me.Uid)
	}
}
