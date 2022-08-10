package vpn

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"
)

// the name is confusing, but we're just getting a generic mocked conn
// that serves as witness of calls
// TODO can copy the mockTLSConn here to avoid confusion with names and
// decouple these tests from those.
func makeTestingClientConn() (*Client, *MockTLSConn) {
	c := makeConnForTransportTest()
	cl := &Client{}
	cl.conn = c
	return cl, c
}

func TestNewClientFromOptions(t *testing.T) {
	t.Run("proper options does not fail getting client", func(t *testing.T) {
		randomFn = func(int) ([]byte, error) {
			return []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}, nil
		}
		opts := makeTestingOptions(t, "AES-128-GCM", "sha512")
		_ = NewClientFromOptions(opts)
	})

	t.Run("nil options return empty client", func(t *testing.T) {
		c := NewClientFromOptions(nil)
		if !reflect.DeepEqual(c, &Client{}) {
			t.Error("Client.NewClientFromOptions(): expected empty client with nil options")
		}
	})

	t.Run("logger gets passed from Options", func(t *testing.T) {
		l := &defaultLogger{}
		globalLogger := logger
		defer func() {
			logger = globalLogger
		}()
		opts := makeTestingOptions(t, "AES-128-GCM", "sha512")
		opts.Log = l
		_ = NewClientFromOptions(opts)
		if logger != l {
			t.Errorf("logger was not overriden")
		}

	})

}

type mockMuxerForClient struct {
	muxer
	writeCalled bool
	readCalled  bool
}

func (mm *mockMuxerForClient) Read([]byte) (int, error) {
	mm.readCalled = true
	return 42, nil
}

func (mm *mockMuxerForClient) Write(b []byte) (int, error) {
	mm.writeCalled = true
	return len(b), nil
}

func mockMuxerFactory() muxFactory {
	fn := func(net.Conn, *Options, *tunnel) (vpnMuxer, error) {
		m := &mockMuxerWithDummyHandshake{}
		return m, nil
	}
	return fn
}

func TestClient_Write(t *testing.T) {
	// test that call to write calls the muxer method
	cl, _ := makeTestingClientConn()
	mux := &mockMuxerForClient{}
	cl.mux = mux
	_, err := cl.Write([]byte("alles ist green"))
	if err != nil {
		t.Errorf("Client.Write(): expected err = nil, got %v", err)
	}
	if !mux.writeCalled {
		t.Errorf("Client.Write(): client.mux.Write() not called")
	}
}

func TestClient_Read(t *testing.T) {
	cl, _ := makeTestingClientConn()
	cl.mux = nil
	b := make([]byte, 255)
	_, err := cl.Read(b)
	if !errors.Is(err, errBadInput) {
		t.Errorf("Client.Read(): nil mux, expected error %v, got %v ", errBadInput, err)
	}

	// test that call to read calls the muxer method
	cl, _ = makeTestingClientConn()
	mux := &mockMuxerForClient{}
	cl.mux = mux
	b = make([]byte, 255)
	_, err = cl.Read(b)
	if err != nil {
		t.Errorf("Client.Read(): expected err = nil, got %v", err)
	}
	if !mux.readCalled {
		t.Errorf("Client.Read(): client.mux.Read() not called")
	}
}

func TestClient_LocalAddr(t *testing.T) {
	cl, _ := makeTestingClientConn()
	cl.tunnel = nil
	a := cl.LocalAddr()
	if a.String() != "" {
		t.Errorf("Client.LocalAddr(): expected empty string, got %v", a.String())
	}
}

func TestClient_RemoteAddr(t *testing.T) {
	cl, _ := makeTestingClientConn()
	a := cl.RemoteAddr()
	if a != nil {
		t.Error("Client.RemoteAddr(): this was not implemented, please fix test")
	}
}

// for the tests that test the delegation of methods to the underlying conn we
// can reuse the mock used in transport_test

func TestClient_SetDeadline(t *testing.T) {
	cl, conn := makeTestingClientConn()
	err := cl.SetDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("Client.SetDeadline() error = %v, want = nil", err)
	}
	if !conn.setDeadlineCalled {
		t.Error("Client.SetDeadline(): conn.SetDeadline() not called")
	}

}

func TestClient_SetReadDeadline(t *testing.T) {
	cl, conn := makeTestingClientConn()
	err := cl.SetReadDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("Client.SetDeadline() error = %v, want = nil", err)
	}
	if !conn.setReadDeadlineCalled {
		t.Error("Client.SetReadDeadline(): conn.SetReadDeadline() not called")
	}
}

func TestClient_SetWriteDeadline(t *testing.T) {
	cl, conn := makeTestingClientConn()
	err := cl.SetWriteDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("Client.SetWriteDeadline() error = %v, want = nil", err)
	}
	if !conn.setWriteDeadlineCalled {
		t.Error("Client.SetWriteDeadline(): conn.SetWriteReadDeadline() not called")
	}
}

func TestClient_Close(t *testing.T) {
	cl, conn := makeTestingClientConn()
	err := cl.Close()
	if err != nil {
		t.Errorf("Client.Close() error = %v, want = nil", err)
	}
	if !conn.closedCalled {
		t.Error("Client.Close(): conn.Close() not called")
	}
}

type badDialer struct{}

func (bd *badDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("cannot dial")
}

func TestClient_DialFailsWithBadOptions(t *testing.T) {
	c := &Client{}
	_, err := c.Dial(context.Background())
	wantErr := errBadInput
	if !errors.Is(err, wantErr) {
		t.Error("Client.Dial(): should fail with nil options")
	}

	c = &Client{
		Opts: &Options{
			Proto: 3,
		},
	}
	_, err = c.Dial(context.Background())
	wantErr = errBadInput
	if !errors.Is(err, wantErr) {
		t.Error("Client.Dial(): should fail with bad proto")
	}

	c = &Client{
		Opts: &Options{
			Proto: TCPMode,
		},
		Dialer: &badDialer{},
	}
	_, err = c.Dial(context.Background())
	wantErr = ErrDialError
	if !errors.Is(err, wantErr) {
		t.Errorf("Client.Dial(): should fail with ErrDialError, err = %v", err)
	}
}

func TestCient_DialRaisesError(t *testing.T) {
	c := &Client{
		Opts: &Options{
			Proto: TCPMode,
		},
	}
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := c.Dial(ctx)
	if err != context.Canceled {
		t.Errorf("Client.Dial(): expected context.Canceled, err = %v", err)
	}
}

func TestClient_StartRaisesDialError(t *testing.T) {
	c := &Client{
		Opts: &Options{
			Proto: TCPMode,
		},
		Dialer: &badDialer{},
	}
	err := c.Start(context.Background())
	if !errors.Is(err, ErrDialError) {
		t.Errorf("Client.Start(): expected = %v, got = %v", ErrDialError, err)
	}
}

func TestClientStartWithMockedMuxerFactory(t *testing.T) {
	c := &Client{
		Opts: &Options{
			Proto: TCPMode,
		},
		Dialer: &mockedDialerContext{},
	}
	c.muxerFactoryFn = mockMuxerFactory()
	err := c.Start(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
