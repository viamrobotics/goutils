package rpc

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	"github.com/pion/mdns"
	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/test"
	"golang.org/x/net/ipv4"

	"go.viam.com/utils/testutils"
)

const (
	testMDNSCandidate = "candidate:1 1 udp 2130706431 0a1b2c3d-1111-4222-8333-444455556666.local 50000 typ host generation 0"
	testIPCandidate   = "candidate:2 1 udp 2130706431 192.168.1.10 50000 typ host generation 0"
)

func TestMDNSCandidateAddress(t *testing.T) {
	name, ok := mdnsCandidateAddress(testMDNSCandidate)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, name, test.ShouldEqual, "0a1b2c3d-1111-4222-8333-444455556666.local")

	name, ok = mdnsCandidateAddress(strings.TrimPrefix(testMDNSCandidate, "candidate:"))
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, name, test.ShouldEqual, "0a1b2c3d-1111-4222-8333-444455556666.local")

	_, ok = mdnsCandidateAddress(testIPCandidate)
	test.That(t, ok, test.ShouldBeFalse)

	_, ok = mdnsCandidateAddress("candidate:3 1 udp 1694498815 203.0.113.5 50000 typ srflx raddr 0.0.0.0 rport 0")
	test.That(t, ok, test.ShouldBeFalse)

	_, ok = mdnsCandidateAddress("not a candidate")
	test.That(t, ok, test.ShouldBeFalse)
}

func TestStripMDNSCandidatesFromSDP(t *testing.T) {
	sdp := strings.Join([]string{
		"v=0",
		"o=- 1 1 IN IP4 0.0.0.0",
		"m=application 9 UDP/DTLS/SCTP webrtc-datachannel",
		"a=" + testIPCandidate,
		"a=" + testMDNSCandidate,
		"a=end-of-candidates",
		"",
	}, "\r\n")

	stripped, cands := stripMDNSCandidatesFromSDP(sdp)
	test.That(t, cands, test.ShouldHaveLength, 1)
	test.That(t, cands[0].Candidate, test.ShouldEqual, testMDNSCandidate)
	test.That(t, stripped, test.ShouldNotContainSubstring, ".local")
	test.That(t, stripped, test.ShouldContainSubstring, "a="+testIPCandidate+"\r\n")
	test.That(t, stripped, test.ShouldContainSubstring, "a=end-of-candidates\r\n")
	test.That(t, strings.Count(stripped, "\r\n"), test.ShouldEqual, strings.Count(sdp, "\r\n")-1)

	unchanged, cands := stripMDNSCandidatesFromSDP(stripped)
	test.That(t, cands, test.ShouldBeEmpty)
	test.That(t, unchanged, test.ShouldEqual, stripped)
}

type fakeCandidateAdder struct {
	mu    sync.Mutex
	added []webrtc.ICECandidateInit
}

func (f *fakeCandidateAdder) AddICECandidate(cand webrtc.ICECandidateInit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, cand)
	return nil
}

func (f *fakeCandidateAdder) candidates() []webrtc.ICECandidateInit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]webrtc.ICECandidateInit(nil), f.added...)
}

func TestAddRemoteICECandidate(t *testing.T) {
	logger := golog.NewTestLogger(t)

	t.Run("ip candidate is added directly", func(t *testing.T) {
		adder := &fakeCandidateAdder{}
		cand := webrtc.ICECandidateInit{Candidate: testIPCandidate}
		test.That(t, addRemoteICECandidate(context.Background(), adder, cand, logger), test.ShouldBeNil)
		test.That(t, adder.candidates(), test.ShouldResemble, []webrtc.ICECandidateInit{cand})
	})

	t.Run("resolvable mDNS candidate is added with its address", func(t *testing.T) {
		testutils.SkipUnlessInternet(t)
		name := uuid.NewString() + ".local"
		udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(224, 0, 0, 0), Port: 5353})
		test.That(t, err, test.ShouldBeNil)
		responder, err := mdns.Server(ipv4.NewPacketConn(udpConn), &mdns.Config{
			LocalNames:      []string{name},
			IncludeLoopback: true,
		})
		test.That(t, err, test.ShouldBeNil)
		defer func() { test.That(t, responder.Close(), test.ShouldBeNil) }()

		adder := &fakeCandidateAdder{}
		mid := "0"
		cand := webrtc.ICECandidateInit{
			Candidate: "candidate:1 1 udp 2130706431 " + name + " 50000 typ host generation 0",
			SDPMid:    &mid,
		}
		test.That(t, addRemoteICECandidate(context.Background(), adder, cand, logger), test.ShouldBeNil)
		testutils.WaitForAssertion(t, func(tb testing.TB) {
			tb.Helper()
			test.That(tb, adder.candidates(), test.ShouldHaveLength, 1)
		})
		added := adder.candidates()[0]
		fields := strings.Fields(added.Candidate)
		test.That(t, fields, test.ShouldHaveLength, len(strings.Fields(cand.Candidate)))
		test.That(t, net.ParseIP(fields[mdnsCandidateAddressField]), test.ShouldNotBeNil)
		test.That(t, added.Candidate, test.ShouldNotContainSubstring, ".local")
		test.That(t, added.SDPMid, test.ShouldEqual, &mid)
	})

	t.Run("unresolvable mDNS candidate is dropped after the timeout", func(t *testing.T) {
		testutils.SkipUnlessInternet(t)
		prevTimeout := mdnsCandidateResolveTimeout
		mdnsCandidateResolveTimeout = 200 * time.Millisecond
		defer func() { mdnsCandidateResolveTimeout = prevTimeout }()

		adder := &fakeCandidateAdder{}
		cand := webrtc.ICECandidateInit{Candidate: "candidate:1 1 udp 2130706431 " + uuid.NewString() + ".local 50000 typ host generation 0"}
		start := time.Now()
		test.That(t, addRemoteICECandidate(context.Background(), adder, cand, logger), test.ShouldBeNil)
		time.Sleep(4 * mdnsCandidateResolveTimeout)
		test.That(t, adder.candidates(), test.ShouldBeEmpty)
		test.That(t, time.Since(start), test.ShouldBeLessThan, 2*time.Second)
	})

	t.Run("resolution stops when the context is canceled", func(t *testing.T) {
		testutils.SkipUnlessInternet(t)
		ctx, cancel := context.WithCancel(context.Background())
		adder := &fakeCandidateAdder{}
		cand := webrtc.ICECandidateInit{Candidate: "candidate:1 1 udp 2130706431 " + uuid.NewString() + ".local 50000 typ host generation 0"}
		test.That(t, addRemoteICECandidate(ctx, adder, cand, logger), test.ShouldBeNil)
		cancel()
		time.Sleep(200 * time.Millisecond)
		test.That(t, adder.candidates(), test.ShouldBeEmpty)
	})
}
