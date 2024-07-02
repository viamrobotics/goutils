package rpc

import (
	"context"
	"sync"
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
	"google.golang.org/grpc/metadata"

	"go.viam.com/utils/testutils"
)

func TestWebRTCServerStreamHeaderRace(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)

	// clientCh is not used directly in the test but the test will leak goroutines if not used.
	clientCh := newWebRTCClientChannel(pc1, dc1, nil, logger, nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()

	server := newWebRTCServer(logger)

	serverCh := newWebRTCServerChannel(server, pc2, dc2, []string{"one", "two"}, logger)
	defer func() {
		test.That(t, serverCh.Close(), test.ShouldBeNil)
	}()

	<-clientCh.Ready()
	<-serverCh.Ready()

	stream := newWebRTCServerStream(context.Background(), nil, "", serverCh, nil, nil, logger)
	defer stream.CloseRecv()

	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		stream.SendHeader(metadata.New(map[string]string{"abc": "def"}))
	}()
	go func() {
		defer wg.Done()
		stream.SetHeader(metadata.New(map[string]string{"hello": "world"}))
	}()
	wg.Wait()
}
