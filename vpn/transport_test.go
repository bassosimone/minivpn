package vpn

import (
	"bytes"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/ooni/minivpn/vpn/mocks"
)

func Test_readPacketFromUDP(t *testing.T) {
	conn := makeTestinConnFromNetwork("udp")
	got, err := readPacketFromUDP(conn)
	want := []byte("alles ist gut")
	if err != nil {
		t.Errorf("readPacketFromUDP() error = %v, want %v", err, nil)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("readPacketFromTCP() got = %s, want %s", got, want)
	}
}

func Test_readPacketFromTCP(t *testing.T) {
	conn := makeTestinConnFromNetwork("tcp")
	got, err := readPacketFromTCP(conn)
	want := []byte("alles ist gut")
	if err != nil {
		t.Errorf("readPacketFromTCP() error = %v, want %v", err, nil)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("readPacketFromTCP() got = %s, want %s", got, want)
	}
}

func Test_readPacket_BadNetwork(t *testing.T) {
	conn := makeTestinConnFromNetwork("unix")
	_, err := readPacket(conn)
	wantErr := ErrBadConnNetwork
	if !errors.Is(err, wantErr) {
		t.Errorf("readPacket() got = %v, want %v", err, wantErr)
	}
}

type MockTLSTransportConn struct {
	*mocks.Conn
	written []byte
}

func makeTestingTLSTransport() (*tlsTransport, *MockTLSTransportConn) {
	readPayload := &packet{opcode: pDataV1, payload: []byte("this is not a payload")}
	s := makeTestingSession()
	a := &mocks.Addr{}
	a.MockNetwork = func() string { return "udp" }
	c := &MockTLSTransportConn{Conn: &mocks.Conn{}}
	c.MockLocalAddr = func() net.Addr { return a }
	c.MockRead = func(b []byte) (int, error) {
		out := readPayload.Bytes()
		copy(b, out)
		return len(out), nil
	}
	c.MockWrite = func(b []byte) (int, error) {
		c.written = b
		return 0, nil
	}
	return &tlsTransport{Conn: c, session: s}, c
}

func Test_tlsTransport_ReadPacket(t *testing.T) {
	fakePayload := append(
		// fake tag
		bytes.Repeat([]byte{0x00}, 13),
		[]byte("this is not a payload")...)
	want := &packet{opcode: pDataV1, payload: fakePayload}

	tt, _ := makeTestingTLSTransport()
	got, err := tt.ReadPacket()

	if err != nil {
		t.Errorf("ReadPacket() error = %v, wantErr %v", err, nil)
	}
	if !bytes.Equal(got.payload, want.payload) {
		t.Errorf("ReadPacket() got = %v, want = %v", got.payload, want.payload)
	}
}

func Test_tlsTransport_WritePacket(t *testing.T) {
	payload := []byte("this is not a payload")
	fakePacket := append([]byte{0x30, 0x02}, bytes.Repeat([]byte{0x00}, 12)...)
	fakePacket = append(fakePacket, payload...)

	tt, conn := makeTestingTLSTransport()
	err := tt.WritePacket(pDataV1, payload)
	if err != nil {
		t.Errorf("ReadPacket() error = %v, want = %v", err, nil)
	}
	if !bytes.Equal(conn.written, fakePacket) {
		t.Errorf("ReadPacket(): got = %v, want = %v", conn.written, fakePacket)
	}
}

func makeTestinConnFromNetwork(network string) net.Conn {
	mockAddr := &mocks.Addr{}
	mockAddr.MockNetwork = func() string {
		return network
	}
	c := &mocks.Conn{}
	c.MockLocalAddr = func() net.Addr {
		return mockAddr
	}
	switch network {
	case "udp":
		c.MockRead = func(b []byte) (int, error) {
			out := []byte("alles ist gut")
			copy(b, out)
			return len(out), nil
		}
	case "tcp":
		c.MockRead = func(b []byte) (int, error) {
			var out []byte
			switch c.Count {
			case 0:
				out = []byte{0x00, 0x0d}
				copy(b, out)
				c.Count += 1
			case 1:
				out = []byte("alles ist gut")
				copy(b, out)
			}
			return len(out), nil
		}
	default:
		c.MockRead = func([]byte) (int, error) {
			return 0, nil
		}
	}
	return c
}

func Test_readPacket(t *testing.T) {

	type args struct {
		conn net.Conn
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr error
	}{
		{
			name: "test read from udp conn is ok",
			args: args{
				conn: makeTestinConnFromNetwork("udp"),
			},
			want:    []byte("alles ist gut"),
			wantErr: nil,
		},
		{
			name: "test read from tcp conn is ok",
			args: args{
				conn: makeTestinConnFromNetwork("tcp"),
			},
			want:    []byte("alles ist gut"),
			wantErr: nil,
		},
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readPacket(tt.args.conn)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("readPacket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("readPacket() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_NewTLSConn(t *testing.T) {
	conn := makeTestinConnFromNetwork("udp")
	s := makeTestingSession()
	_, err := NewTLSConn(conn, s)
	if err != nil {
		t.Errorf("NewTLSConn() error = %v, want = nil", err)
	}
}

type MockTLSConn struct {
	mocks.Conn
	closedCalled           bool
	localAddrCalled        bool
	remoteAddrCalled       bool
	setDeadlineCalled      bool
	setReadDeadlineCalled  bool
	setWriteDeadlineCalled bool
}

func makeConnForTransportTest() *MockTLSConn {
	localAddr := &mocks.Addr{}
	localAddr.MockString = func() string { return "1.1.1.1" }
	localAddr.MockNetwork = func() string { return "udp" }

	remoteAddr := &mocks.Addr{}
	remoteAddr.MockString = func() string { return "2.2.2.2" }
	remoteAddr.MockNetwork = func() string { return "udp" }

	c := &MockTLSConn{}
	c.MockClose = func() error {
		c.closedCalled = true
		return nil
	}
	c.MockLocalAddr = func() net.Addr {
		c.localAddrCalled = true
		return localAddr
	}
	c.MockRemoteAddr = func() net.Addr {
		c.remoteAddrCalled = true
		return remoteAddr
	}
	c.MockSetDeadline = func(time.Time) error {
		c.setDeadlineCalled = true
		return nil
	}
	c.MockSetReadDeadline = func(time.Time) error {
		c.setReadDeadlineCalled = true
		return nil
	}
	c.MockSetWriteDeadline = func(time.Time) error {
		c.setWriteDeadlineCalled = true
		return nil
	}
	return c
}

func makeTestingTLSConn() (*TLSConn, *MockTLSConn) {
	c := makeConnForTransportTest()
	t := &TLSConn{}
	t.conn = c
	return t, c
}

func TestTLSConn_Read_Fails_With_Bad_Data(t *testing.T) {
	tc, _ := makeTestingTLSConn()
	b := make([]byte, 16)
	_, err := tc.Read(b)
	wantErr := errBadInput
	if !errors.Is(err, wantErr) {
		t.Errorf("TLSConn.Read(): empty session; gotErr = %v, wantErr = %v ", err, wantErr)
	}

}

func TestTLSConn_Read(t *testing.T) {
	// call witnesses
	readFromConnCalled := false
	readFromQueueCalled := false

	payload := []byte("alles ist gut")

	// setup the fields we need
	tc, _ := makeTestingTLSConn()
	tc.session = makeTestingSession()
	ackQueue := make(chan *packet, 16)
	tc.session.ackQueue = ackQueue

	// mock read functions
	tc.doReadFromConnFn = func(tcn *TLSConn, b []byte) (bool, int, error) {
		readFromConnCalled = true
		copy(b[:], payload)
		return true, len(payload), nil
	}
	tc.doReadFromQueueFn = func(tcn *TLSConn, b []byte) (bool, int, error) {
		readFromQueueCalled = true
		copy(b[:], payload)
		return true, len(payload), nil
	}

	// first we read from conn

	b := make([]byte, 255)
	n, err := tc.Read(b)

	if err != nil {
		t.Errorf("TLSConn.Read(): expected no error, got %v", err)
	}
	if n != len(payload) {
		t.Errorf("TLSConn.Read(): readFromConn returned wrong len %v", n)
	}
	if !readFromConnCalled {
		t.Errorf("TLSConn.Read(): readFromConn not called")
	}
	if readFromQueueCalled {
		t.Errorf("TLSConn.Read(): readFromQueue should have not been called")
	}

	// now we read from queue. reset the witnesses:

	readFromConnCalled = false
	readFromQueueCalled = false

	// inject one packet in the queue
	p := &packet{opcode: pDataV1, payload: []byte("alles ist gut")}
	tc.session.ackQueue <- p

	b = make([]byte, 255)

	// and do another call to Read()
	n, err = tc.Read(b)
	if err != nil {
		t.Errorf("TLSConn.Read(): expected no error, got %v", err)
	}
	if !readFromQueueCalled {
		t.Errorf("TLSConn.Read(): readFromQueue not called")
	}
	if readFromConnCalled {
		t.Errorf("TLSConn.Read(): readFromConn should not have been called")
	}
}

func makeTestingTLSTransportFromPayload(payload []byte) (*tlsTransport, *MockTLSTransportConn) {
	s := makeTestingSession()
	a := &mocks.Addr{}
	a.MockNetwork = func() string { return "udp" }
	c := &MockTLSTransportConn{Conn: &mocks.Conn{}}
	c.MockLocalAddr = func() net.Addr { return a }
	c.MockRead = func(b []byte) (int, error) {
		out := payload
		copy(b, out)
		return len(out), nil
	}
	c.MockWrite = func(b []byte) (int, error) {
		c.written = b
		return 0, nil
	}
	return &tlsTransport{Conn: c, session: s}, c
}

func makePacketForTLSConnTest(id int, s *session) *packet {
	p := &packet{
		id:              packetID(id),
		opcode:          pControlV1,
		keyID:           0x00,
		payload:         []byte("aaa"),
		localSessionID:  s.LocalSessionID,
		remoteSessionID: s.RemoteSessionID,
		acks:            []packetID{},
	}
	return p
}

func makeTestingTLSConnForReadTest(payload []byte) *TLSConn {
	tc, _ := makeTestingTLSConn()
	tt, _ := makeTestingTLSTransportFromPayload(payload)
	tc.transport = tt
	tc.session = makeTestingSession()
	ackQueue := make(chan *packet, 16)
	tc.session.ackQueue = ackQueue
	return tc
}

func Test_doReadFromConn(t *testing.T) {
	s := makeTestingSession()
	p := makePacketForTLSConnTest(1, s) // next packet
	payload := p.Bytes()

	tc := makeTestingTLSConnForReadTest(payload)
	sendACKFn = func(net.Conn, *session, packetID) error {
		return nil
	}
	writeAndReadFromBufferFn = func(*bytes.Buffer, []byte, []byte) (int, error) {
		return 42, nil
	}
	b := make([]byte, 255)
	ok, n, err := doReadFromConn(tc, b)
	if err != nil {
		t.Errorf("doReadFromBuffer(): wanted error=%v, got=%v", nil, err)
		return
	}
	if !ok {
		t.Errorf("doReadFromBuffer(): expected ok=true, got ok=%v", ok)
		return
	}
	if n != 42 {
		t.Errorf("doReadFromBuffer(): expected %v, got %v", 42, n)
	}
	if len(tc.session.ackQueue) != 0 {
		t.Errorf("doReadFromBuffer(): ackQueue should be 0")
	}
}

func Test_doReadFromConn_Out_Of_Order_Packet(t *testing.T) {
	s := makeTestingSession()
	p := makePacketForTLSConnTest(2, s) // not next packet
	payload := p.Bytes()

	tc := makeTestingTLSConnForReadTest(payload)

	sendACKFn = func(net.Conn, *session, packetID) error {
		return nil
	}
	writeAndReadFromBufferFn = func(*bytes.Buffer, []byte, []byte) (int, error) {
		return 42, nil
	}
	b := make([]byte, 255)
	ok, n, err := doReadFromConn(tc, b)
	if err != nil {
		t.Errorf("doReadFromBuffer(): wanted error=%v, got=%v", nil, err)
		return
	}
	if ok {
		t.Errorf("doReadFromBuffer(): expected ok=false, got ok=%v", ok)
		return
	}
	if n != 0 {
		t.Errorf("doReadFromBuffer(): expected %v, got %v", 0, n)
	}
	if len(tc.session.ackQueue) != 1 {
		t.Errorf("doReadFromBuffer(): ackQueue should be 1")
	}
}

func Test_doReadFromConn_Bubble_Up_Errors(t *testing.T) {
	s := makeTestingSession()
	p := makePacketForTLSConnTest(1, s) // next packet
	payload := p.Bytes()

	tc := makeTestingTLSConnForReadTest(payload)

	makeUpError := errors.New("silly error")

	sendACKFn = func(net.Conn, *session, packetID) error {
		return makeUpError
	}
	writeAndReadFromBufferFn = func(*bytes.Buffer, []byte, []byte) (int, error) {
		return 42, nil
	}
	b := make([]byte, 255)
	_, _, err := doReadFromConn(tc, b)
	if !errors.Is(err, makeUpError) {
		t.Errorf("doReadFromBuffer(): wanted error=%v, got=%v", makeUpError, err)
		return
	}
}

func Test_doReadFromQueue(t *testing.T) {
	s := makeTestingSession()
	p := makePacketForTLSConnTest(2, s)            // not next packet
	tc := makeTestingTLSConnForReadTest(p.Bytes()) // dont care, not going to use it
	tc.session.ackQueue <- p

	// mock ack and writes
	sendACKFn = func(net.Conn, *session, packetID) error {
		return nil
	}
	writeAndReadFromBufferFn = func(*bytes.Buffer, []byte, []byte) (int, error) {
		return 42, nil
	}
	b := make([]byte, 255)
	_, _, err := doReadFromQueue(tc, b)
	if err != nil {
		t.Errorf("doReadFromQueue(): wanted error=%v, got=%v", nil, err)
	}

}

func TestTLSConn_doRead(t *testing.T) {
	tt, _ := makeTestingTLSTransport()
	tc := &TLSConn{transport: tt}
	_, err := tc.doRead()
	if err != nil {
		t.Errorf("TLSConn.doRead(): expected nil error")
		return
	}

	tc = &TLSConn{}
	_, err = tc.doRead()
	if !errors.Is(err, errBadInput) {
		t.Errorf("TLSConn.doRead(): should fail with nil transport. got: %v, wanted: %v", err, errBadInput)
		return
	}

}

func TestTLSConn_canRead(t *testing.T) {
	tc := &TLSConn{
		session: makeTestingSession(),
	}
	canRead := tc.canRead(nil)
	if canRead {
		t.Errorf("TLSConn.canRead() should return false with nil packet")
	}

	pNext := &packet{id: 1}
	canRead = tc.canRead(pNext)
	if !canRead {
		t.Errorf("TLSConn.canRead() should be able to read pID = 1")
	}

	pEq := &packet{id: 0}
	canRead = tc.canRead(pEq)
	if canRead {
		t.Errorf("TLSConn.canRead() should not able to read pID = 0")
	}

	tc.session.localPacketID = packetID(42)
	pMore := &packet{id: 44}
	canRead = tc.canRead(pMore)
	if canRead {
		t.Errorf("TLSConn.canRead() should not able to read pID = 44")
	}

	pLess := &packet{id: 41}
	canRead = tc.canRead(pLess)
	if canRead {
		t.Errorf("TLSConn.canRead() should not able to read pID = 41")
	}
}

func Test_writeAndReadFromBuffer(t *testing.T) {
	bb := &bytes.Buffer{}
	b := make([]byte, 255)
	payload := []byte("this test is green")
	n, err := writeAndReadFromBuffer(bb, b, payload)
	if err != nil {
		t.Error("writeAndReadFromBuffer(): expected no error")
	}
	if n != len(payload) {
		t.Errorf("writeAndReadFromBuffer(): got len = %v, wanted = %v", n, len(payload))
	}
}

func TestTLSConn_Close(t *testing.T) {
	tc, conn := makeTestingTLSConn()
	err := tc.Close()
	if err != nil {
		t.Errorf("TLSConn.Close() error = %v, want = nil", err)
	}
	if !conn.closedCalled {
		t.Error("TLSConn.Close(): conn.Close() not called")
	}
}

func TestTLSConn_LocalAddr(t *testing.T) {
	tc, conn := makeTestingTLSConn()
	want := "1.1.1.1"
	if addr := tc.LocalAddr(); addr.String() != want {
		t.Errorf("TLSConn.LocalAddr() got = %s, want = %s", addr, want)
	}
	if !conn.localAddrCalled {
		t.Error("TLSConn.LocalAddr(): conn.LocalAddr() not called")
	}
}

func TestTLSConn_RemoteAddr(t *testing.T) {
	tc, conn := makeTestingTLSConn()
	want := "2.2.2.2"
	if addr := tc.RemoteAddr(); addr.String() != want {
		t.Errorf("TLSConn.RemoteAddr() got = %s, want = %s", addr, want)
	}
	if !conn.remoteAddrCalled {
		t.Error("TLSConn.RemoteAddr(): conn.RemoteAddr() not called")
	}
}

func TestTLSConn_SetDeadline(t *testing.T) {
	tc, conn := makeTestingTLSConn()
	err := tc.SetDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("TLSConn.SetDeadline() error = %v, want = nil", err)
	}
	if !conn.setDeadlineCalled {
		t.Error("TLSConn.SetDeadline(): conn.SetDeadline() not called")
	}
}

func TestTLSConn_SetReadDeadline(t *testing.T) {
	tc, conn := makeTestingTLSConn()
	err := tc.SetReadDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("TLSConn.SetReadDeadline() error = %v, want = nil", err)
	}
	if !conn.setReadDeadlineCalled {
		t.Error("TLSConn.SetReadDeadline(): conn.SetReadDeadline() not called")
	}
}

func TestTLSConn_SetWriteDeadline(t *testing.T) {
	tc, conn := makeTestingTLSConn()
	err := tc.SetWriteDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("TLSConn.SetWriteDeadline() error = %v, want = nil", err)
	}
	if !conn.setWriteDeadlineCalled {
		t.Error("TLSConn.SetWriteDeadline(): conn.SetWriteDeadline() not called")
	}
}

func TestTLSConn_Write(t *testing.T) {
	a := &mocks.Addr{}
	a.MockNetwork = func() string { return "udp" }
	conn := &mocks.Conn{}
	conn.MockLocalAddr = func() net.Addr { return a }
	c := &MockTLSTransportConn{Conn: conn}
	c.MockWrite = func(b []byte) (int, error) {
		c.written = b
		return len(b), nil
	}
	s := makeTestingSession()
	tlsTr := &tlsTransport{Conn: c, session: s}
	tc := &TLSConn{transport: tlsTr, session: s}

	payload := []byte("this is fine")
	want := append(
		[]byte{0x20, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		payload...)
	_, err := tc.Write(payload)
	if err != nil {
		t.Errorf("TLSConn.Write(): expected err = nil, got = %v", err)
	}
	if !bytes.Equal(c.written, want) {
		t.Errorf("TLSConn.Write(): written = %v, want = %v", c.written, want)
	}
}
