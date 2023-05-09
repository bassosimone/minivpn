package vpn

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	tls "github.com/refraction-networking/utls"
)

//
// OpenVPN Multiplexer
//

/*
 The vpnMuxer interface represents the VPN transport multiplexer.

 One important limitation of the current implementation at this moment is that
 the processing of incoming packets needs to be driven by reads from the user of
 the library. This means that if you don't do reads during some time, any packets
 on the control channel that the server sends us (e.g., openvpn-pings) will not
 be processed (and so, not acknowledged) until triggered by a muxer.Read().

 From the original documentation:
 https://community.openvpn.net/openvpn/wiki/SecurityOverview

 "OpenVPN multiplexes the SSL/TLS session used for authentication and key
 exchange with the actual encrypted tunnel data stream. OpenVPN provides the
 SSL/TLS connection with a reliable transport layer (as it is designed to
 operate over). The actual IP packets, after being encrypted and signed with an
 HMAC, are tunnelled over UDP without any reliability layer. So if --proto udp
 is used, no IP packets are tunneled over a reliable transport, eliminating the
 problem of reliability-layer collisions -- Of course, if you are tunneling a
 TCP session over OpenVPN running in UDP mode, the TCP protocol itself will
 provide the reliability layer."

 SSL/TLS -> Reliability Layer -> \
            --tls-auth HMAC       \
                                   \
                                    > Multiplexer ----> UDP/TCP
                                   /                    Transport
 IP        Encrypt and HMAC       /
 Tunnel -> using OpenSSL EVP --> /
 Packets   interface.

"This model has the benefit that SSL/TLS sees a reliable transport layer while
the IP packet forwarder sees an unreliable transport layer -- exactly what both
components want to see. The reliability and authentication layers are
completely independent of one another, i.e. the sequence number is embedded
inside the HMAC-signed envelope and is not used for authentication purposes."

*/

// muxer implements vpnMuxer
type muxer struct {

	// A net.Conn that has access to the "wire" transport. this can
	// represent an UDP/TCP socket, or a net.Conn coming from a Pluggable
	// Transport etc.
	conn net.Conn

	// control and data are the handlers for the control and data channels.
	// they implement the methods needed for the handshake and handling of
	// packets.
	control controlHandler
	data    dataHandler

	// bufReader is used to buffer data channel reads. We only write to
	// this buffer when we have correctly decrypted an incoming packet.
	bufReader *bytes.Buffer

	reliable *reliableTransport

	// Mutable state tied to a concrete session.
	// session *session

	// Mutable state tied to a particular vpn run.
	tunnel *tunnelInfo

	// Options are OpenVPN options that come from parsing a subset of the OpenVPN
	// configuration directives, plus some non-standard config directives.
	options *Options

	// eventListener is a channel to which Event_*- will be sent if
	// the channel is not nil.
	eventListener chan uint8

	failed bool
}

var _ vpnMuxer = &muxer{} // Ensure that we implement the vpnMuxer interface.

//
// Interfaces
//

// vpnMuxer contains all the behavior expected by the muxer.
type vpnMuxer interface {
	Handshake(ctx context.Context) error
	Reset(net.Conn, *reliableTransport) error
	InitDataWithRemoteKey(net.Conn) error
	SetEventListener(chan uint8)
	Write([]byte) (int, error)
	Read([]byte) (int, error)
	Stop()
}

// controlHandler manages the control "channel".
type controlHandler interface {
	SendHardReset(net.Conn, *reliableTransport) error
	ParseHardReset([]byte) (sessionID, error)
	SendACK(net.Conn, *reliableTransport, packetID) error
	PushRequest() []byte
	ReadPushResponse([]byte) map[string][]string
	ControlMessage(*reliableTransport, *Options) ([]byte, error)
	ReadControlMessage([]byte) (*keySource, string, error)
}

// dataHandler manages the data "channel".
type dataHandler interface {
	SetupKeys(*dataChannelKey) error
	SetPeerID(int) error
	WritePacket(net.Conn, []byte) (int, error)
	ReadPacket(*packet) ([]byte, error)
	DecodeEncryptedPayload([]byte, *dataChannelState) (*encryptedData, error)
	EncryptAndEncodePayload([]byte, *dataChannelState) ([]byte, error)
}

//
// muxer initialization
//

// muxFactory acepts a net.Conn, a pointer to an Options object, and another
// pointer to a tunnelInfo object, and returns a vpnMuxer and an error if it
// could not be initialized. This type is used to be able to mock a muxer while
// testing the Client.
type muxFactory func(conn net.Conn, options *Options, tunnel *tunnelInfo) (vpnMuxer, error)

// newMuxerFromOptions returns a configured muxer, and any error if the
// operation could not be completed.
func newMuxerFromOptions(conn net.Conn, options *Options, tunnel *tunnelInfo) (vpnMuxer, error) {
	control := &control{}
	sess, err := newSession()
	if err != nil {
		return &muxer{}, err
	}
	reliable := newReliableTransport(sess)
	reliable.start()
	data, err := newDataFromOptions(options, sess)
	if err != nil {
		return &muxer{}, err
	}
	br := bytes.NewBuffer(nil)

	m := &muxer{
		conn:      conn,
		reliable:  reliable,
		options:   options,
		control:   control,
		data:      data,
		tunnel:    tunnel,
		bufReader: br,
	}
	return m, nil
}

// stop the transport

func (m *muxer) Stop() {
	m.reliable.stop()
}

//
// observability
//

// SetEvenSetEventListener assigns the passed channel as the event listener for
// this muxer.
func (m *muxer) SetEventListener(el chan uint8) {
	m.eventListener = el
}

// emit sends the passed stage into any configured EventListener
func (m *muxer) emit(stage uint8) {
	select {
	case m.eventListener <- stage:
	default:
		// do not deliver
	}
}

//
// muxer handshake
//

// Handshake performs the OpenVPN "handshake" operations serially. Accepts a
// Context, and it returns any error that is raised at any of the underlying
// steps.
func (m *muxer) Handshake(ctx context.Context) (err error) {
	errch := make(chan error, 1)
	go func() {
		errch <- m.handshake()
	}()
	select {
	case err = <-m.reliable.errChan:
	case err = <-errch:
	case <-ctx.Done():
		err = ctx.Err()
		m.failed = true
	}
	return
}

func (m *muxer) handshake() error {

	// 1. control channel sends reset, parse response.

	m.emit(EventReset)

	for {
		if err := m.Reset(m.conn, m.reliable); err == nil {
			break
		}
	}

	// 2. TLS handshake.

	// TODO(ainghazal): move the initialization step to an early phase and keep a ref in the muxer
	if !m.options.hasAuthInfo() {
		return fmt.Errorf("%w: %s", errBadInput, "expected certificate or username/password")
	}
	certCfg, err := newCertConfigFromOptions(m.options)
	if err != nil {
		return err
	}

	var tlsConf *tls.Config

	tlsConf, err = initTLSFn(certCfg)
	if err != nil {
		logger.Errorf("%w: %s", ErrBadTLSInit, err)
		return err
	}

	// TODO - we just need the reliable transport here
	/*
		tlsConn, err := newControlChannelTLSConn(m.conn, m.reliable)
		if err != nil {
				return fmt.Errorf("%w: %s", ErrBadTLSHandshake, err)
			    }
		fmt.Println(tlsConn)
	*/

	m.emit(EventTLSHandshake)

	// After completing the TLS handshake, we get a tls transport that implements
	// net.Conn. The subsequente call to InitDataWithRemoteKey needs to pass this TLS context.
	var tls net.Conn

	// TODO: we need to make reliable *borrow* the underlying connection
	m.reliable.Conn = m.conn
	// TODO(ainghazal): figure out the proper TLS retry/timeout parameter by default
	timeoutTLS := time.NewTicker(10 * time.Second)
	defer func() {
		timeoutTLS.Stop()
	}()
	tls, err = tlsHandshakeFn(m.reliable, tlsConf)
	if err != nil {
		logger.Error(fmt.Errorf("%w: %s", ErrBadTLSHandshake, err).Error())
		return err
	}

	logger.Info("TLS handshake done")
	m.emit(EventTLSHandshakeDone)

	// 3. data channel init (auth, push, data initialization).

	if err := m.InitDataWithRemoteKey(tls); err != nil {
		return fmt.Errorf("%w: %s", ErrBadDataHandshake, err)

	}
	m.emit(EventDataInitDone)

	logger.Info("VPN handshake done")
	m.reliable.doneHandshake <- struct{}{}
	return nil
}

// Reset sends a hard-reset packet to the server, and awaits the server
// confirmation.
func (m *muxer) Reset(conn net.Conn, r *reliableTransport) error {
	if m.control == nil {
		return fmt.Errorf("%w: %s", errBadInput, "bad control")
	}
	if err := m.control.SendHardReset(conn, r); err != nil {
		return err
	}

	var resp []byte
	var err error
	var remoteSessionID sessionID
	const goodHardResetResponseLen = 26
	for {
		if m.failed {
			return errors.New("cannot read server reset")
		}
		resp, err = readPacket(m.conn)
		if err != nil {
			// TODO(ainghazal): seems to be a bug here, after UDP retry timeouts we don't exit the loop.
			//log.Println("error getting packet")
			continue
		}
		if len(resp) != goodHardResetResponseLen {
			continue
		}
		remoteSessionID, err = m.control.ParseHardReset(resp)
		// here we could check if we have received a remote session id but
		// our session.remoteSessionID is != from all zeros
		r.session.RemoteSessionID = remoteSessionID
		break
	}

	logger.Infof("Remote session ID: %x", r.session.RemoteSessionID)
	logger.Infof("Local session ID:  %x", r.session.LocalSessionID)

	// we assume id is 0, this is the first packet we ack.
	// XXX I could parse the real packet id from server instead. this
	// _might_ be important when re-keying?
	return m.control.SendACK(m.conn, r, packetID(0))
}

//
// muxer: read and handle packets
//

// handleIncoming packet reads the next packet available in the underlying
// socket. It returns true if the packet was a data packet; otherwise it will
// process it but return false.
// TODO(ainghazal, bassosimone): this function partially overlaps with the function of the same
// name in reliableTransport
func (m *muxer) handleIncomingPacket(data []byte) (bool, error) {
	panicIfTrue(m.data == nil, "muxer not initialized")
	var input []byte
	if data == nil {
		parsed, err := readPacket(m.conn)
		if err != nil {
			return false, err
		}
		input = parsed
	} else {
		input = data
	}

	if isPing(input) {
		err := handleDataPing(m.conn, m.data)
		if err != nil {
			logger.Errorf("cannot handle ping: %s", err.Error())
		}
		return false, nil
	}

	var p *packet
	var err error

	if p, err = parsePacketFromBytes(input); err != nil {
		logger.Error(err.Error())
		return false, err
	}
	if p.isControl() {
		logger.Infof("Got control packet, should handle: %d", len(data))
		// Here the server might be requesting us to reset, or to
		// re-key (but I keep ignoring that case for now).
		// we're doing nothing for now.
		fmt.Println(hex.Dump(p.payload))
		return false, nil
	}
	if p.isACK() {
		logger.Infof("Got ACK")
		return false, nil
	}
	if !p.isData() {
		fmt.Printf("Unhandled packet (non-data): %v\n", p)
		return false, nil
	}

	// at this point, the incoming packet should be
	// a data packet that needs to be processed
	// (decompress+decrypt)

	plaintext, err := m.data.ReadPacket(p)
	if err != nil {
		logger.Errorf("%s", err.Error())
		return false, err
	}

	// all good! we write the plaintext into the read buffer.
	// the caller is responsible for reading from there.
	m.bufReader.Write(plaintext)
	return true, nil
}

// handleDataPing replies to an openvpn-ping with a canned response.
func handleDataPing(conn net.Conn, data dataHandler) error {
	log.Println("openvpn-ping, sending reply")
	if data == nil {
		return fmt.Errorf("%w: %s", errBadInput, "null data handler")
	}
	_, err := data.WritePacket(conn, pingPayload)
	return err
}

// readTLSPacket reads a packet over the TLS connection.
func (m *muxer) readTLSPacket(tls net.Conn) ([]byte, error) {
	panicIfTrue(tls == nil, "tls is nil")
	data := make([]byte, 4096)
	_, err := tls.Read(data)
	return data, err
}

// readAndLoadRemoteKey reads one incoming TLS packet, and tries to parse the
// response contained in it. If the server response is the right kind of
// packet, it will store the remote key and the parts of the remote options
// that will be of use later.
func (m *muxer) readAndLoadRemoteKey(tls net.Conn) error {
	data, err := m.readTLSPacket(tls)
	if err != nil {
		return err
	}
	if !isControlMessage(data) {
		return fmt.Errorf("%w: %s", errBadControlMessage, "expected null header")
	}

	// Parse the received data: we expect remote key and remote options.
	remoteKey, remoteOptStr, err := m.control.ReadControlMessage(data)
	if err != nil {
		logger.Errorf("cannot parse control message")
		return fmt.Errorf("%w: %s", ErrBadHandshake, err)
	}

	// Store the remote key.
	key, err := m.reliable.session.ActiveKey()
	if err != nil {
		logger.Errorf("cannot get active key")
		return fmt.Errorf("%w: %s", ErrBadHandshake, err)
	}
	err = key.addRemoteKey(remoteKey)
	if err != nil {
		logger.Errorf("cannot add remote key")
		return fmt.Errorf("%w: %s", ErrBadHandshake, err)
	}

	// Parse and update the useful fields from the remote options (mtu).
	ti := newTunnelInfoFromRemoteOptionsString(remoteOptStr)
	m.tunnel.mtu = ti.mtu
	return nil
}

// sendPushRequest sends a push request over the TLS channel.
func (m *muxer) sendPushRequest(tls net.Conn) (int, error) {
	return tls.Write(m.control.PushRequest())
}

// readPushReply reads one incoming TLS packet, where we expect to find the
// response to our push request. If the server response is the right kind of
// packet, it will store the parts of the pushed options that will be of use
// later.
func (m *muxer) readPushReply(tls net.Conn) error {
	panicIfTrue(m.control == nil || m.tunnel == nil, "muxer badly initialized")

	resp, err := m.readTLSPacket(tls)
	if err != nil {
		return err
	}

	logger.Info("Server pushed options")

	if isBadAuthReply(resp) {
		return errBadAuth
	}

	if !isPushReply(resp) {
		return fmt.Errorf("%w:%s", errBadServerReply, "expected push reply")
	}

	optsMap := m.control.ReadPushResponse(resp)
	ti := newTunnelInfoFromPushedOptions(optsMap)

	m.tunnel.ip = ti.ip
	m.tunnel.gw = ti.gw
	m.tunnel.peerID = ti.peerID

	logger.Infof("Tunnel IP: %s", m.tunnel.ip)
	logger.Infof("Gateway IP: %s", m.tunnel.gw)
	logger.Infof("Peer ID: %d", m.tunnel.peerID)

	return nil
}

// sendControl message sends a control message over the TLS channel.
func (m *muxer) sendControlMessage(tls net.Conn) error {
	cm, err := m.control.ControlMessage(m.reliable, m.options)
	if err != nil {
		return err
	}

	if _, err := tls.Write(cm); err != nil {
		return err
	}
	return nil
}

// InitDataWithRemoteKey initializes the internal data channel. To do that, it sends a
// control packet, parses the response, and derives the cryptographic material
// that will be used to encrypt and decrypt data through the tunnel. At the end
// of this exchange, the data channel is ready to be used.
func (m *muxer) InitDataWithRemoteKey(tls net.Conn) error {

	// 1. first we send a control message.

	if err := m.sendControlMessage(tls); err != nil {
		return err
	}

	// 2. then we read the server response and load the remote key.

	for {
		if err := m.readAndLoadRemoteKey(tls); err == nil {
			break
		}
	}

	// 3. now we can initialize the data channel.

	key0, err := m.reliable.session.ActiveKey()
	if err != nil {
		return err
	}

	err = m.data.SetupKeys(key0)
	if err != nil {
		return err
	}

	// 4. finally, we ask the server to push remote options to us. we parse
	// them and keep some useful info.

	if _, err := m.sendPushRequest(tls); err != nil {
		logger.Errorf("error sending: %v", err)
		return err
	}

	for {
		if err := m.readPushReply(tls); err != nil {
			i := rand.Intn(500)
			fmt.Printf("error reading push reply: %v. sleeping: %vms\n", err, i)
			time.Sleep(time.Millisecond * time.Duration(i))
			continue
		}
		break
	}

	m.data.SetPeerID(m.tunnel.peerID)

	return nil
}

// Write sends user bytes as encrypted packets in the data channel. It returns
// the number of written bytes, and an error if the operation could not succeed.
func (m *muxer) Write(b []byte) (int, error) {
	panicIfTrue(m.data == nil, "muxer: data not initialized")
	return m.data.WritePacket(m.conn, b)
}

// Read reads bytes after decrypting packets from the data channel. This is the
// user-view of the VPN connection reads. It returns the number of bytes read,
// and an error if the operation could not succeed.
func (m *muxer) Read(b []byte) (int, error) {
	for {
		ok, err := m.handleIncomingPacket(nil)
		if err != nil {
			return 0, err
		}
		if ok {
			break
		}
	}
	return m.bufReader.Read(b)
}

var (
	ErrBadHandshake     = errors.New("bad vpn handshake")
	ErrBadDataHandshake = errors.New("bad data handshake")
)
